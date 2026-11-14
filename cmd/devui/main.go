// Command devui serves the desktop UI in a normal browser for development and screenshots.
// It proxies UI calls to the running LocalSites service. Listens on 127.0.0.1 only, and only answers
// calls from a browser that opened the link it prints (it has the app's full rights, so other programs
// on this Mac must not be able to use it).
//
//	go run ./cmd/devui   then open the printed http://127.0.0.1:7788/?t=… link
package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/localsites/localsites/internal/daemon"
	"github.com/localsites/localsites/internal/engine"
	"github.com/localsites/localsites/internal/secrets"
)

const cookieName = "lsdevui"

func main() {
	self, _ := os.Executable()
	if len(os.Args) > 1 {
		self = os.Args[1] // path to the localsites binary used to start the service
	}
	c, err := daemon.Ensure(self)
	if err != nil {
		log.Fatal(err)
	}
	token := os.Getenv("LOCALSITES_DEVUI_TOKEN") // tests set their own
	if len(token) < 20 {
		token = secrets.Generate(32)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /__call/{method}", func(w http.ResponseWriter, r *http.Request) {
		if ck, err := r.Cookie(cookieName); err != nil || subtle.ConstantTimeCompare([]byte(ck.Value), []byte(token)) != 1 {
			http.Error(w, "open the link devui printed", http.StatusForbidden)
			return
		}
		if !strings.HasPrefix(r.Host, "127.0.0.1:") { // basic DNS-rebinding guard
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		// Any website open in the browser can POST here too: only accept calls from this page.
		if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if s := r.Header.Get("Sec-Fetch-Site"); s != "" && s != "same-origin" && s != "none" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var p any
		_ = json.Unmarshal(body, &p)
		res, err := c.As("devui").Call(r.PathValue("method"), p)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			out := map[string]any{"error": err.Error()}
			var fe engine.FieldErrors
			if errors.As(err, &fe) {
				out["fields"] = fe // same shape the desktop app gets
			}
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		_, _ = w.Write(res)
	})
	addr := os.Getenv("LOCALSITES_DEVUI_ADDR") // tests use a random port
	if addr == "" {
		addr = "127.0.0.1:7788"
	}
	ui := os.Getenv("LOCALSITES_DEVUI_DIR")
	if ui == "" {
		ui = "desktop/frontend/dist"
	}
	files := http.FileServer(http.Dir(ui))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if t := r.URL.Query().Get("t"); t != "" && subtle.ConstantTimeCompare([]byte(t), []byte(token)) == 1 {
			http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		files.ServeHTTP(w, r)
	})
	log.Println("UI dev server on http://" + addr + "/?t=" + token)
	log.Fatal(http.ListenAndServe(addr, mux))
}
