// Package frontdoor gives each site its https://name.test address.
//
// Two drivers:
//   - valet:   when Laravel Valet is installed, reuse its nginx (:80/:443) and DNS, adding a
//     proxy per site. Valet sites keep working side by side.
//   - builtin: LocalSites' own HTTPS reverse proxy + .test DNS server (for machines without Valet).
package frontdoor

import "fmt"

// Route maps a domain to the backend URL.
type Route struct {
	Domain  string // "shop.test"
	Backend string // "https://127.0.0.1:8744"
}

// Driver is a front door implementation.
type Driver interface {
	Name() string
	// Sync makes the routes active and removes routes this driver created earlier that are gone.
	Sync(routes []Route) (log []string, err error)
	// Owns reports whether another program already serves this domain (conflict check).
	Conflict(domain string) string
	Running() bool
}

// LogError formats an error line for the apply log.
func LogError(what string, err error) string { return fmt.Sprintf("%s: %v", what, err) }
