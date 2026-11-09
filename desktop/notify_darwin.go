//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation -framework UserNotifications
#include <stdlib.h>
#import <Foundation/Foundation.h>
#import <UserNotifications/UserNotifications.h>

// Asks once for permission to show notifications. macOS only prompts the first time.
static void lsNotifyAuthorize(void) {
	[[UNUserNotificationCenter currentNotificationCenter]
		requestAuthorizationWithOptions:(UNAuthorizationOptionAlert | UNAuthorizationOptionSound | UNAuthorizationOptionBadge)
		completionHandler:^(BOOL granted, NSError *error) {}];
}

static void lsNotifySend(const char *title, const char *body) {
	NSString *t = [NSString stringWithUTF8String:title];
	NSString *b = [NSString stringWithUTF8String:body];
	UNUserNotificationCenter *center = [UNUserNotificationCenter currentNotificationCenter];
	[center requestAuthorizationWithOptions:(UNAuthorizationOptionAlert | UNAuthorizationOptionSound | UNAuthorizationOptionBadge)
		completionHandler:^(BOOL granted, NSError *error) {
			if (!granted) return;
			UNMutableNotificationContent *content = [[UNMutableNotificationContent alloc] init];
			content.title = t;
			content.body = b;
			UNNotificationRequest *req = [UNNotificationRequest requestWithIdentifier:[[NSUUID UUID] UUIDString]
				content:content trigger:nil];
			[center addNotificationRequest:req withCompletionHandler:nil];
		}];
}
*/
import "C"

import (
	"os"
	"strings"
	"unsafe"

	"github.com/localsites/localsites/internal/notify"
)

// inBundle reports whether this binary runs from inside LocalSites.app. macOS only shows the app's own name
// and icon on a notification when it comes from the app bundle; otherwise we keep the osascript fallback.
func inBundle() bool {
	exe, err := os.Executable()
	return err == nil && strings.Contains(exe, ".app/Contents/MacOS/")
}

// useNativeNotifications sends notifications as LocalSites (with its icon) instead of through osascript,
// which macOS shows as coming from Script Editor.
func useNativeNotifications() {
	if !inBundle() || os.Getenv("LOCALSITES_HOME") != "" {
		return
	}
	notify.Send = func(title, text string) {
		ct, cb := C.CString(title), C.CString(text)
		defer C.free(unsafe.Pointer(ct))
		defer C.free(unsafe.Pointer(cb))
		C.lsNotifySend(ct, cb)
	}
}

// askNotificationPermission shows the macOS permission prompt early, from the app window, so the first
// notification isn't lost while the prompt is open.
func askNotificationPermission() {
	if inBundle() {
		C.lsNotifyAuthorize()
	}
}
