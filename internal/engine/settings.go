package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"sort"
	"strings"
	"time"

	"github.com/localsites/localsites/internal/config"
	"github.com/localsites/localsites/internal/runtime"
	"github.com/localsites/localsites/internal/services"
	"github.com/localsites/localsites/internal/version"
)

// PHPIniKeys are the php.ini settings that can be changed in Settings (safe, commonly needed ones).
var labelRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,39}$`)

var PHPIniKeys = map[string]*regexp.Regexp{
	"memory_limit":        regexp.MustCompile(`^(-1|\d{1,5}[KMG]?)$`),
	"upload_max_filesize": regexp.MustCompile(`^\d{1,5}[KMG]?$`),
	"post_max_size":       regexp.MustCompile(`^\d{1,5}[KMG]?$`),
	"max_execution_time":  regexp.MustCompile(`^\d{1,5}$`),
	"max_input_time":      regexp.MustCompile(`^-?\d{1,5}$`),
	"max_input_vars":      regexp.MustCompile(`^\d{1,7}$`),
	"display_errors":      regexp.MustCompile(`^(On|Off)$`),
	"error_reporting":     regexp.MustCompile(`^[A-Z_&~| ^0-9]{1,80}$`),
	"output_buffering":    regexp.MustCompile(`^(On|Off|\d{1,7})$`),
	"short_open_tag":      regexp.MustCompile(`^(On|Off)$`),
	"date.timezone":       regexp.MustCompile(`^[A-Za-z_]+(/[A-Za-z0-9_+-]+){0,2}$`),
}

// SettingsView is everything the Settings screen shows.
type SettingsView struct {
	config.Settings
	Version       string            `json:"version"`
	PHPDefaults   map[string]string `json:"php_defaults"`  // LocalSites built-in values
	PHPEffective  map[string]string `json:"php_effective"` // defaults + user values
	PHPIniKeys    []string          `json:"php_ini_keys"`
	StartAtLogin  bool              `json:"start_at_login"`
	LoginSupport  bool              `json:"start_at_login_supported"`
	DataDir       string            `json:"data_dir"`
	LogsDir       string            `json:"logs_dir"`
	BackupsDir    string            `json:"backups_dir"`
	MySQLSocket   string            `json:"mysql_socket"`
	ValetFound    bool              `json:"valet_found"`
	FrontDoorUsed string            `json:"front_door_used"`
}

func (e *Engine) Settings() SettingsView {
	cfg := e.Store.Get()
	v := SettingsView{Settings: cfg.Settings, Version: version.Version, PHPDefaults: services.PHPDefaults,
		PHPEffective: e.phpIni(), StartAtLogin: e.startAtLogin(), LoginSupport: goruntime.GOOS == "darwin",
		DataDir: e.P.Data, LogsDir: e.P.Logs, BackupsDir: e.P.Backups, MySQLSocket: e.P.Socket("mysql"), FrontDoorUsed: e.Front.Name()}
	_, v.ValetFound = runtime.Valet()
	for k := range PHPIniKeys {
		v.PHPIniKeys = append(v.PHPIniKeys, k)
	}
	sort.Strings(v.PHPIniKeys)
	return v
}

// phpIni merges LocalSites defaults with the user's global values.
func (e *Engine) phpIni() map[string]string {
	out := map[string]string{}
	for k, v := range services.PHPDefaults {
		out[k] = v
	}
	for k, v := range e.Store.Get().Settings.PHPIni {
		out[k] = v
	}
	return out
}

// SettingsPatch changes some settings. nil = unchanged.
type SettingsPatch struct {
	SitesRoot    *string            `json:"sites_root,omitempty"`
	Roots        *[]config.Root     `json:"roots,omitempty"`
	FrontDoor    *string            `json:"front_door,omitempty"`
	ApachePort   *int               `json:"apache_port,omitempty"`
	NginxPort    *int               `json:"nginx_port,omitempty"`
	MySQLPort    *int               `json:"mysql_port,omitempty"`
	HTTPPort     *int               `json:"http_port,omitempty"`
	HTTPSPort    *int               `json:"https_port,omitempty"`
	DNSPort      *int               `json:"dns_port,omitempty"`
	DefaultPHP   *string            `json:"default_php,omitempty"`
	PHPIni       map[string]*string `json:"php_ini,omitempty"` // value nil or "" = back to the default
	StartAtLogin *bool              `json:"start_at_login,omitempty"`
	MySQLAlways  *bool              `json:"mysql_always_on,omitempty"`
	UpdateAuto   *bool              `json:"update_auto_check,omitempty"`
	UpdateMode   *string            `json:"update_mode,omitempty"`
	UpdateChan   *string            `json:"update_channel,omitempty"`
	UpdateAsked  *bool              `json:"update_asked,omitempty"`
	UpdateSkip   *string            `json:"update_skip,omitempty"`
	UpdateLater  *string            `json:"update_later,omitempty"`
	ReportMode   *string            `json:"report_mode,omitempty"`
}

// onlyUpdates reports whether the patch only changes update settings.
func (p SettingsPatch) onlyUpdates() bool {
	b, _ := json.Marshal(p)
	var m map[string]json.RawMessage
	_ = json.Unmarshal(b, &m)
	for k := range m {
		if !strings.HasPrefix(k, "update_") && k != "report_mode" {
			return false
		}
	}
	return len(m) > 0
}

// FieldError points at the setting that is wrong, so the UI can show it next to the field.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type FieldErrors []FieldError

func (f FieldErrors) Error() string {
	var s []string
	for _, e := range f {
		s = append(s, e.Field+": "+e.Message)
	}
	return strings.Join(s, "; ")
}

// UpdateSettings validates, saves and applies settings. It returns what was done.
func (e *Engine) UpdateSettings(p SettingsPatch) ([]string, error) {
	cfg := e.Store.Get()
	s := cfg.Settings
	var errs FieldErrors
	bad := func(field, msg string) { errs = append(errs, FieldError{field, msg}) }

	if p.SitesRoot != nil {
		root := strings.TrimSpace(*p.SitesRoot)
		if strings.HasPrefix(root, "~/") {
			root = filepath.Join(e.P.Home, root[2:])
		}
		if st, err := os.Stat(root); err != nil || !st.IsDir() || !filepath.IsAbs(root) {
			bad("sites_root", "Choose an existing folder")
		} else {
			s.SitesRoot = filepath.Clean(root)
			if len(s.Roots) > 0 {
				s.Roots[0].Path = s.SitesRoot
			}
		}
	}
	if p.Roots != nil {
		var roots []config.Root
		labels, paths := map[string]bool{}, map[string]bool{}
		for i, r := range *p.Roots {
			field := fmt.Sprintf("roots.%d", i)
			path := strings.TrimSpace(r.Path)
			if strings.HasPrefix(path, "~/") {
				path = filepath.Join(e.P.Home, path[2:])
			}
			path = filepath.Clean(path)
			label := strings.TrimSpace(r.Label)
			if label == "" {
				label = filepath.Base(path)
			}
			switch {
			case !filepath.IsAbs(path) || !isDir(path):
				bad(field, "Choose an existing folder")
			case !labelRe.MatchString(label):
				bad(field, "Labels may use letters, digits, dash and underscore")
			case labels[strings.ToLower(label)]:
				bad(field, "Two folders can't have the same label")
			case paths[path]:
				bad(field, "This folder is already in the list")
			case r.Layout != "" && r.Layout != "auto" && r.Layout != "flat" && r.Layout != "groups":
				bad(field, "Layout must be auto, flat or groups")
			}
			labels[strings.ToLower(label)], paths[path] = true, true
			roots = append(roots, config.Root{Path: path, Label: label, Layout: r.Layout})
		}
		if len(roots) == 0 {
			bad("roots", "Keep at least one projects folder")
		}
		s.Roots = roots
		if len(roots) > 0 {
			s.SitesRoot = roots[0].Path
		}
	}
	if p.FrontDoor != nil {
		switch *p.FrontDoor {
		case "auto", "valet", "builtin":
			s.FrontDoor = *p.FrontDoor
		default:
			bad("front_door", "Choose Automatic, Laravel Valet or Built-in")
		}
		if *p.FrontDoor == "valet" {
			if _, ok := runtime.Valet(); !ok {
				bad("front_door", "Laravel Valet is not installed")
			}
		}
	}
	port := func(field string, in *int, cur *int, allowLow bool) {
		if in == nil || *in == *cur {
			return
		}
		min := 1024
		if allowLow {
			min = 1
		}
		if *in < min || *in > 65535 {
			bad(field, fmt.Sprintf("Use a port between %d and 65535", min))
			return
		}
		if l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *in)); err != nil {
			bad(field, fmt.Sprintf("Port %d is already in use", *in))
			return
		} else {
			l.Close()
		}
		*cur = *in
	}
	port("apache_port", p.ApachePort, &s.ApachePort, false)
	port("mysql_port", p.MySQLPort, &s.MySQLPort, false)
	port("nginx_port", p.NginxPort, &s.NginxPort, false)
	port("http_port", p.HTTPPort, &s.HTTPPort, true)
	port("https_port", p.HTTPSPort, &s.HTTPSPort, true)
	if p.DNSPort != nil && *p.DNSPort != s.DNSPort {
		if *p.DNSPort < 1024 || *p.DNSPort > 65535 {
			bad("dns_port", "Use a port between 1024 and 65535")
		} else {
			s.DNSPort = *p.DNSPort
		}
	}
	used := map[int]string{}
	nginx := s.NginxPort
	if nginx == 0 {
		nginx = 8746
	}
	for f, v := range map[string]int{"apache_port": s.ApachePort, "mysql_port": s.MySQLPort, "http_port": s.HTTPPort, "https_port": s.HTTPSPort, "nginx_port": nginx} {
		if other, dup := used[v]; dup {
			bad(f, "Same port as "+strings.ReplaceAll(other, "_", " "))
		}
		used[v] = f
	}
	if p.DefaultPHP != nil {
		if *p.DefaultPHP != "" {
			if _, ok := runtime.FindPHP(*p.DefaultPHP); !ok {
				bad("default_php", "PHP "+*p.DefaultPHP+" is not installed")
			}
		}
		s.DefaultPHP = *p.DefaultPHP
	}
	if p.MySQLAlways != nil {
		s.MySQLAlwaysOn = *p.MySQLAlways
	}
	if p.UpdateAuto != nil {
		s.UpdateAutoCheck, s.UpdateAsked = *p.UpdateAuto, true
	}
	if p.UpdateAsked != nil {
		s.UpdateAsked = *p.UpdateAsked
	}
	if p.UpdateMode != nil {
		switch *p.UpdateMode {
		case "notify", "download", "auto":
			s.UpdateMode = *p.UpdateMode
		default:
			bad("update_mode", "Choose notify, download or auto")
		}
	}
	if p.UpdateChan != nil {
		switch *p.UpdateChan {
		case "stable", "beta":
			s.UpdateChannel = *p.UpdateChan
		default:
			bad("update_channel", "Choose stable or beta")
		}
	}
	if p.ReportMode != nil {
		switch *p.ReportMode {
		case "off", "ask", "auto":
			s.ReportMode = *p.ReportMode
		default:
			bad("report_mode", "Choose off, ask or auto")
		}
	}
	if p.UpdateSkip != nil {
		s.UpdateSkip = strings.TrimSpace(*p.UpdateSkip)
	}
	if p.UpdateLater != nil {
		s.UpdateLater = strings.TrimSpace(*p.UpdateLater)
	}
	if p.PHPIni != nil {
		ini := map[string]string{}
		for k, v := range s.PHPIni {
			ini[k] = v
		}
		for k, v := range p.PHPIni {
			re, ok := PHPIniKeys[k]
			if !ok {
				bad("php_ini."+k, "This setting can't be changed here")
				continue
			}
			if v == nil || strings.TrimSpace(*v) == "" {
				delete(ini, k)
				continue
			}
			val := strings.TrimSpace(*v)
			if !re.MatchString(val) {
				bad("php_ini."+k, "Not a valid value")
				continue
			}
			ini[k] = val
		}
		s.PHPIni = ini
	}
	if len(errs) > 0 {
		return nil, errs
	}

	var log []string
	if p.StartAtLogin != nil && *p.StartAtLogin != e.startAtLogin() {
		if err := e.setStartAtLogin(*p.StartAtLogin); err != nil {
			return nil, FieldErrors{{"start_at_login", err.Error()}}
		}
		log = append(log, map[bool]string{true: "LocalSites will start at login", false: "LocalSites won't start at login"}[*p.StartAtLogin])
	}

	old := cfg.Settings
	if err := e.Store.Update(func(c *config.Config) error { c.Settings = s; return nil }); err != nil {
		return nil, err
	}
	if p.onlyUpdates() { // update choices change nothing that runs
		return log, nil
	}
	if s.FrontDoor != old.FrontDoor || s.HTTPPort != old.HTTPPort || s.HTTPSPort != old.HTTPSPort || s.DNSPort != old.DNSPort {
		if b, ok := e.Front.(interface{ Stop() }); ok {
			b.Stop()
		}
		e.Front = e.pickFrontDoor(s)
		log = append(log, "Domains now use "+e.Front.Name())
	}
	if s.MySQLPort != old.MySQLPort && e.Sup.Status(procMySQL).Running {
		e.Sup.Stop(procMySQL, 30*time.Second)
		log = append(log, "MySQL restarted on port "+fmt.Sprint(s.MySQLPort))
	}
	if s.ApachePort != old.ApachePort && e.Sup.Status(procApache).Running {
		e.Sup.Stop(procApache, 15*time.Second) // a new Listen port needs a full restart
	}
	if s.NginxPort != old.NginxPort && e.Sup.Status(procNginx).Running {
		e.Sup.Stop(procNginx, 10*time.Second)
	}
	return append(log, e.Apply()...), nil
}

/* ---------------------------------------------------------- start at login */

const loginLabel = "dev.localsites.agent"

func (e *Engine) launchAgentPath() string {
	dir := os.Getenv("LOCALSITES_LAUNCHAGENTS_DIR") // tests use a temp dir
	if dir == "" {
		dir = filepath.Join(e.P.Home, "Library", "LaunchAgents")
	}
	return filepath.Join(dir, loginLabel+".plist")
}

func (e *Engine) startAtLogin() bool {
	if goruntime.GOOS != "darwin" {
		return false
	}
	_, err := os.Stat(e.launchAgentPath())
	return err == nil
}

// setStartAtLogin installs or removes a per-user LaunchAgent that runs `<this binary> daemon` at login.
// Only called when the user flips the switch in Settings or runs the CLI command.
func (e *Engine) setStartAtLogin(on bool) error {
	if goruntime.GOOS != "darwin" {
		return errors.New("start at login is available on macOS for now")
	}
	file := e.launchAgentPath()
	sandboxed := os.Getenv("LOCALSITES_LAUNCHAGENTS_DIR") != ""
	uid := fmt.Sprint(os.Getuid())
	if !on {
		if !sandboxed {
			_ = exec.Command("/bin/launchctl", "bootout", "gui/"+uid+"/"+loginLabel).Run()
		}
		if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	self, _ = filepath.EvalSymlinks(self)
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key><array><string>%s</string><string>daemon</string></array>
  <key>RunAtLoad</key><true/>
  <key>EnvironmentVariables</key><dict><key>PATH</key><string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string></dict>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, loginLabel, xmlEscape(self), xmlEscape(filepath.Join(e.P.Logs, "login.log")), xmlEscape(filepath.Join(e.P.Logs, "login.log")))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, []byte(plist), 0o644) // loaded by macOS at the next login
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
