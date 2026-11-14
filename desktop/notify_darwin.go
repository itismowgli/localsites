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

// lsNotifySendWait sends one notification and waits (up to 5 s) until macOS has it, for the short-lived
// "LocalSites notify" helper process. Returns 0 when it was handed over.
static int lsNotifySendWait(const char *title, const char *body) {
	NSString *t = [NSString stringWithUTF8String:title];
	NSString *b = [NSString stringWithUTF8String:body];
	UNUserNotificationCenter *center = [UNUserNotificationCenter currentNotificationCenter];
	dispatch_semaphore_t done = dispatch_semaphore_create(0);
	__block int result = 1;
	[center requestAuthorizationWithOptions:(UNAuthorizationOptionAlert | UNAuthorizationOptionSound | UNAuthorizationOptionBadge)
		completionHandler:^(BOOL granted, NSError *error) {
			if (!granted) { result = 2; dispatch_semaphore_signal(done); return; }
			UNMutableNotificationContent *content = [[UNMutableNotificationContent alloc] init];
			content.title = t;
			content.body = b;
			UNNotificationRequest *req = [UNNotificationRequest requestWithIdentifier:[[NSUUID UUID] UUIDString]
				content:content trigger:nil];
			[center addNotificationRequest:req withCompletionHandler:^(NSError *err) {
				result = err == nil ? 0 : 1;
				dispatch_semaphore_signal(done);
			}];
		}];
	dispatch_semaphore_wait(done, dispatch_time(DISPATCH_TIME_NOW, 5 * NSEC_PER_SEC));
	return result;
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
	"path/filepath"
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

// notifyOnce is "LocalSites notify <title> <text>": a service started from the command line runs it so the
// notification shows LocalSites' name and icon. Exits non-zero when macOS didn't take it (the caller then
// falls back to osascript). A refusal of permission counts as handled: the person said no.
func notifyOnce(title, text string) int {
	if !inBundle() {
		return 1
	}
	ct, cb := C.CString(title), C.CString(text)
	defer C.free(unsafe.Pointer(ct))
	defer C.free(unsafe.Pointer(cb))
	switch C.lsNotifySendWait(ct, cb) {
	case 0, 2:
		return 0
	}
	return 1
}

// rememberAppPath writes where this app is, so the service can send notifications through it.
func rememberAppPath(dataDir string) {
	exe, err := os.Executable()
	if err != nil || !inBundle() || os.Getenv("LOCALSITES_HOME") != "" {
		return
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	_ = os.MkdirAll(dataDir, 0o700)
	_ = os.WriteFile(filepath.Join(dataDir, "app-path"), []byte(exe+"\n"), 0o600)
}

// askNotificationPermission shows the macOS permission prompt early, from the app window, so the first
// notification isn't lost while the prompt is open.
func askNotificationPermission() {
	if inBundle() {
		C.lsNotifyAuthorize()
	}
}
