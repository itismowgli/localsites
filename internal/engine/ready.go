package engine

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// waitReady blocks until every PHP pool socket accepts connections and Apache serves each
// domain from its own virtual host (not the "No site here" default), or the timeout passes.
func waitReady(apachePort int, sockets, domains []string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for _, sock := range sockets {
		for {
			c, err := net.DialTimeout("unix", sock, 500*time.Millisecond)
			if err == nil {
				c.Close()
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("PHP pool %s is not accepting connections", sock)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{
		Proxy: nil,
		// Internal hop to our own Apache on 127.0.0.1; its certificate is checked by the front door.
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		DisableKeepAlives: true,
	}}
	url := fmt.Sprintf("https://127.0.0.1:%d/", apachePort)
	for _, d := range domains {
		for {
			req, _ := http.NewRequest(http.MethodHead, url, nil)
			req.Host = d
			res, err := client.Do(req)
			if err == nil {
				io.Copy(io.Discard, res.Body)
				res.Body.Close()
				if res.Header.Get("X-LocalSites-Default") == "" {
					break
				}
			}
			if time.Now().After(deadline) {
				if err != nil {
					return fmt.Errorf("Apache is not answering: %v", err)
				}
				return fmt.Errorf("Apache has not loaded %s yet", d)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	return nil
}

// removeStaleSockets deletes the listen sockets named in a php-fpm config. A crashed master leaves
// them behind and a new master refuses to start while they exist. Read at launch time, so pools
// added after the first start are covered too.
func removeStaleSockets(fpmConf string) {
	b, err := os.ReadFile(fpmConf)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(k) == "listen" {
			_ = os.Remove(strings.Trim(strings.TrimSpace(v), `"`))
		}
	}
}
