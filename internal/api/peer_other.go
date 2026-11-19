//go:build !darwin

package api

import "net"

// Linux and Windows have no app signature to check: any program running as the person can say it's the
// app, so the caller's own name is used (see TrustedClient).
func canProveApp() bool { return false }

func peerIsSelf(net.Conn) bool { return false }
