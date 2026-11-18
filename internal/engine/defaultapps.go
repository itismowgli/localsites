package engine

import (
	"errors"
	"fmt"
	"strings"

	"github.com/localsites/localsites/internal/apps"
	"github.com/localsites/localsites/internal/config"
)

// The editor and terminal the Open buttons use. The person picks them; nothing is guessed. "ask" means
// show the chooser every time.

// AskEveryTime is the saved value for "let me choose each time".
const AskEveryTime = "ask"

// AppList is apps.list: the installed apps, each marked when it is the person's default for its kind.
type AppList struct {
	Apps     []apps.App        `json:"apps"`
	Defaults map[string]string `json:"defaults"` // kind -> app id or "ask"; a kind is missing until chosen
}

// InstalledApps lists the apps on this computer with the saved defaults.
func (e *Engine) InstalledApps() AppList {
	d := map[string]string{}
	for k, v := range e.Store.Get().Settings.Apps {
		d[k] = v
	}
	return AppList{Apps: apps.Installed(), Defaults: d}
}

// SetDefaultApp saves the default app for a kind. id "" forgets the choice; "ask" asks every time.
func (e *Engine) SetDefaultApp(kind, id string) error {
	if !apps.IsDefaultKind(kind) {
		return errors.New("kind must be editor or terminal")
	}
	if id != "" && id != AskEveryTime {
		a, err := apps.Find(id)
		if err != nil {
			return err
		}
		if a.Kind != kind {
			return fmt.Errorf("%s is not a %s", a.Name, kindName(kind))
		}
	}
	return e.Store.Update(func(c *config.Config) error {
		if c.Settings.Apps == nil {
			c.Settings.Apps = map[string]string{}
		}
		if id == "" {
			delete(c.Settings.Apps, kind)
		} else {
			c.Settings.Apps[kind] = id
		}
		return nil
	})
}

// ResolveApp turns "editor" or "terminal" into the chosen app. Other ids pass through. When nothing
// usable is chosen, the error lists what is installed so a person (or an agent asking one) can pick.
func (e *Engine) ResolveApp(id string) (string, error) {
	if !apps.IsDefaultKind(id) {
		return id, nil
	}
	kind := id
	saved := e.Store.Get().Settings.Apps[kind]
	if saved != "" && saved != AskEveryTime {
		if _, err := apps.Find(saved); err == nil {
			return saved, nil
		}
	}
	var names []string
	for _, a := range apps.Installed() {
		if a.Kind == kind {
			names = append(names, fmt.Sprintf("%s (%s)", a.Name, a.ID))
		}
	}
	if len(names) == 0 {
		return "", fmt.Errorf("no %s found on this computer", kindName(kind))
	}
	why := "No " + kindName(kind) + " chosen yet"
	switch {
	case saved == AskEveryTime:
		why = "You chose to pick the " + kindName(kind) + " each time"
	case saved != "":
		why = "The chosen " + kindName(kind) + " (" + saved + ") is no longer installed"
	}
	return "", fmt.Errorf("%s. Installed: %s. Open again with app set to one of these ids (remember: true makes it the default)", why, strings.Join(names, ", "))
}

func kindName(kind string) string {
	if kind == "editor" {
		return "code editor"
	}
	return kind
}
