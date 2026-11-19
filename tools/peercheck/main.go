// peercheck calls the LocalSites service as "app" and asks it to decide an approval that doesn't exist, to
// see whether the service accepts it as the app (signed builds check the caller's signature). Tests only.
package main

import (
	"fmt"

	"github.com/localsites/localsites/internal/api"
	"github.com/localsites/localsites/internal/paths"
)

func main() {
	c, err := api.Dial(paths.Default())
	if err != nil {
		fmt.Println("dial:", err)
		return
	}
	_, err = c.As("app").Call("approvals.decide", map[string]any{"id": "none", "approve": false})
	fmt.Println(err)
}
