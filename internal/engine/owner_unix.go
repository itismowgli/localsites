//go:build unix

package engine

import (
	"os"
	"syscall"
)

// fileOwner is the user id that owns a file.
func fileOwner(info os.FileInfo) (uint32, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return st.Uid, true
}
