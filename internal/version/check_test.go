package version

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheck(t *testing.T) {
	if !less("0.9.0", "0.10.0") || less("0.10.0", "0.9.0") || less("1.0.0", "1.0.0") {
		t.Fatal("version compare")
	}
	tag := "v99.0.0"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tag == "" {
			w.WriteHeader(404)
			return
		}
		fmt.Fprintf(w, `{"tag_name":%q,"html_url":"https://github.com/x/releases/%s"}`, tag, tag)
	}))
	defer srv.Close()
	old := ReleasesURL
	ReleasesURL = srv.URL
	defer func() { ReleasesURL = old }()
	if u := Check(); !u.Newer || u.Latest != "99.0.0" {
		t.Fatalf("newer: %+v", u)
	}
	tag = "v" + Version
	if u := Check(); u.Newer {
		t.Fatalf("same version: %+v", u)
	}
	tag = ""
	if u := Check(); u.Newer || u.Message == "" {
		t.Fatalf("no release: %+v", u)
	}
}
