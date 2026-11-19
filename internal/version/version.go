// Package version holds the app version and the What's new feed shown in the app.
package version

import (
	_ "embed"
	"encoding/json"
)

// Version of LocalSites (semantic versioning).
const Version = "1.6.1"

//go:embed changelog.json
var changelogJSON []byte

// Entry is one release in the What's new feed.
type Entry struct {
	Version string   `json:"version"`
	Date    string   `json:"date"`
	Title   string   `json:"title"`
	Items   []string `json:"items"`
	// Kinds labels each item, in order: new, improved, fixed or security. Release notes and CHANGELOG.md
	// group the items by it; the app shows the plain list.
	Kinds []string `json:"kinds,omitempty"`
}

// Changelog returns releases, newest first. Later this is merged with a signed remote feed.
func Changelog() []Entry {
	var e []Entry
	_ = json.Unmarshal(changelogJSON, &e)
	return e
}
