//go:build !darwin

package main

func useNativeNotifications()           {}
func askNotificationPermission()        {}
func notifyOnce(title, text string) int { return 1 }
func rememberAppPath(dataDir string)    {}
