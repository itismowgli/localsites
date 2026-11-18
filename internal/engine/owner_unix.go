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

// fileDevice is the device (disk) a file is on.
func fileDevice(info os.FileInfo) (uint64, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Dev), true //nolint:unconvert // int32 on macOS, uint64 on Linux
}
