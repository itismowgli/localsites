//go:build !darwin

package main

func useNativeNotifications()                      {}
func askNotificationPermission()                   {}
func notifyOnce(title, text, kind, ref string) int { return 1 }
func listenForNoticeButtons()                      {}
func rememberAppPath(dataDir string)               {}
