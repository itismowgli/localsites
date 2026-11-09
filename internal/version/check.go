package version

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Repo is where releases are published.
const Repo = "itismowgli/localsites"

// Update is the result of an update check.
type Update struct {
	Current string `json:"current"`
	Latest  string `json:"latest,omitempty"`
	Newer   bool   `json:"newer"`
	URL     string `json:"url,omitempty"`
	Message string `json:"message"`
}

// ReleasesURL is replaceable in tests.
var ReleasesURL = "https://api.github.com/repos/" + Repo + "/releases/latest"

// Check asks GitHub for the latest release. It only reads; installing stays a deliberate step.
func Check() Update {
	u := Update{Current: Version}
	c := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequest("GET", ReleasesURL, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := c.Do(req)
	if err != nil {
		u.Message = "Couldn't reach GitHub. Check your connection and try again."
		return u
	}
	defer res.Body.Close()
	if res.StatusCode == 404 {
		u.Message = "No public release yet. You're on the newest build from source."
		return u
	}
	if res.StatusCode != 200 {
		u.Message = fmt.Sprintf("GitHub answered %s. Try again later.", res.Status)
		return u
	}
	var r struct {
		Tag string `json:"tag_name"`
		URL string `json:"html_url"`
	}
	_ = json.NewDecoder(res.Body).Decode(&r)
	u.Latest, u.URL = strings.TrimPrefix(r.Tag, "v"), r.URL
	u.Newer = less(Version, u.Latest)
	if u.Newer {
		u.Message = "LocalSites " + u.Latest + " is available."
	} else {
		u.Message = "You're up to date."
	}
	return u
}

// less compares dotted versions numerically (0.9.0 < 0.10.0).
func less(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x < y
		}
	}
	return false
}
