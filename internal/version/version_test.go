package version

import (
	"strings"
	"testing"
)

// Every release from 1.0.0 on labels each item, so release notes and CHANGELOG.md can group them.
func TestEveryReleaseLabelsItsItems(t *testing.T) {
	ok := map[string]bool{"new": true, "improved": true, "fixed": true, "security": true}
	for _, e := range Changelog() {
		if strings.HasPrefix(e.Version, "0.") {
			continue
		}
		if len(e.Kinds) != len(e.Items) {
			t.Errorf("%s: %d items but %d kinds", e.Version, len(e.Items), len(e.Kinds))
		}
		for i, k := range e.Kinds {
			if !ok[k] {
				t.Errorf("%s item %d: kind %q (new, improved, fixed or security)", e.Version, i, k)
			}
		}
	}
}
