package importers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The lines that matter from the httpd.conf and httpd-vhosts.conf that free MAMP ships (MAMP 7, macOS),
// with /Applications/MAMP replaced by a temporary folder. XAMPP ships Apache's same stock files.
const mampMain = `#
# ServerRoot: The top of the directory tree under which the server's
ServerRoot "{root}/Library"
Listen 8888
LoadModule php5_module        {root}/bin/php/php5.4.45/modules/libphp5.so
# DocumentRoot: The directory out of which you will serve your
DocumentRoot "{root}/htdocs"
<Directory "{root}/htdocs">
</Directory>
# Virtual hosts
#Include {root}/conf/apache/extra/httpd-vhosts.conf
`

const stockVhosts = `NameVirtualHost *:80
<VirtualHost *:80>
    ServerAdmin webmaster@dummy-host.example.com
    DocumentRoot "{root}/Library/docs/dummy-host.example.com"
    ServerName dummy-host.example.com
</VirtualHost>
<VirtualHost *:80>
    ServerAdmin webmaster@dummy-host2.example.com
    DocumentRoot "{root}/Library/docs/dummy-host2.example.com"
    ServerName dummy-host2.example.com
</VirtualHost>
`

func mampFixture(t *testing.T, main, vhosts string) Source {
	t.Helper()
	root := t.TempDir()
	put := func(rel, body string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(strings.ReplaceAll(body, "{root}", root)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put("conf/apache/httpd.conf", main)
	put("conf/apache/extra/httpd-vhosts.conf", vhosts)
	for _, d := range []string{"htdocs/shop", "htdocs/blog", "htdocs/.cache"} {
		_ = os.MkdirAll(filepath.Join(root, d), 0o755)
	}
	put("htdocs/index.php", "<?php phpinfo();")
	return Source{ID: "mamp", Name: "MAMP", HostConfs: []string{filepath.Join(root, "conf/apache/extra/httpd-vhosts.conf")},
		MainConf: filepath.Join(root, "conf/apache/httpd.conf")}
}

func TestFreeMAMPDefaults(t *testing.T) {
	s := mampFixture(t, mampMain, stockVhosts)
	hosts := s.Hosts()
	if len(hosts) != 2 {
		t.Fatalf("want the 2 htdocs folders, got %+v", hosts)
	}
	for _, h := range hosts {
		if strings.Contains(h.Docroot, "dummy-host") {
			t.Fatalf("Apache's example host was imported: %+v", h)
		}
		if h.PHP != "5.4.45" || h.Source != "mamp" {
			t.Fatalf("host = %+v", h)
		}
	}
	if filepath.Base(hosts[0].Docroot) != "blog" || filepath.Base(hosts[1].Docroot) != "shop" {
		t.Fatalf("hosts = %+v", hosts)
	}
	doc, _, _ := readMain(s.MainConf)
	if filepath.Base(doc) != "htdocs" {
		t.Fatalf("docroot = %q", doc)
	}
}

func TestMAMPWithVirtualHostsSwitchedOn(t *testing.T) {
	main := strings.Replace(mampMain, "#Include {root}/conf", "Include {root}/conf", 1)
	vhosts := stockVhosts + `<VirtualHost *:80>
    DocumentRoot "/Users/me/Sites/shop/public"
    ServerName shop.local
</VirtualHost>
`
	s := mampFixture(t, main, vhosts)
	hosts := s.Hosts()
	var names []string
	for _, h := range hosts {
		names = append(names, h.Name+"="+filepath.Base(h.Docroot))
	}
	got := strings.Join(names, ",")
	if !strings.Contains(got, "shop.local=public") || strings.Contains(got, "dummy") || len(hosts) != 3 {
		t.Fatalf("hosts = %s", got)
	}
}

func TestIncludeRelativeToServerRoot(t *testing.T) {
	// XAMPP style: Include etc/extra/httpd-vhosts.conf, relative to ServerRoot
	root := t.TempDir()
	conf := filepath.Join(root, "etc", "httpd.conf")
	_ = os.MkdirAll(filepath.Dir(conf), 0o755)
	_ = os.WriteFile(conf, []byte(`ServerRoot "`+root+`"
DocumentRoot "`+root+`/htdocs"
Include etc/extra/httpd-vhosts.conf
`), 0o644)
	doc, inc, php := readMain(conf)
	if !inc[filepath.Join(root, "etc", "extra", "httpd-vhosts.conf")] || doc != filepath.Join(root, "htdocs") || php != "" {
		t.Fatalf("doc %q includes %v php %q", doc, inc, php)
	}
}

func TestMAMPProStillReadsItsHosts(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "httpd.conf")
	_ = os.WriteFile(f, []byte(`<VirtualHost *:80>
	ServerName shop.test
	DocumentRoot "/Users/me/Sites/shop"
	<FilesMatch "\.php$">
		SetHandler "proxy:unix:/Applications/MAMP/Library/logs/fastcgi/php8.2.0.fcgi.sock|fcgi://localhost"
	</FilesMatch>
</VirtualHost>
`), 0o644)
	s := Source{ID: "mamp-pro", HostConfs: []string{f}}
	h := s.Hosts()
	if len(h) != 1 || h[0].Name != "shop.test" || h[0].PHP != "8.2.0" {
		t.Fatalf("hosts = %+v", h)
	}
}
