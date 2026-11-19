//go:build linux

package main

// linuxTray: the desktop shows tray icons, so the app can live there like in the macOS menu bar.
func linuxTray() bool { return trayAvailable() }
