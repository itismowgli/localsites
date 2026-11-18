//go:build !unix

package engine

import "os"

// fileOwner: there are no Unix owners here, so nothing counts as owned by someone else.
func fileOwner(os.FileInfo) (uint32, bool) { return 0, false }

// fileDevice: no device numbers here.
func fileDevice(os.FileInfo) (uint64, bool) { return 0, false }
