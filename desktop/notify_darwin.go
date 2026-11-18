//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation -framework UserNotifications
#include <stdlib.h>
void lsNotifyAuthorize(void);
void lsNotifyListen(void);
int lsNotifySendWait(const char *title, const char *body, const char *kind, const char *ref);
void lsNotifySend(const char *title, const char *body, const char *kind, const char *ref);
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
	notify.SendNotice = func(title, text, kind, ref string) {
		ct, cb, ck, cr := C.CString(title), C.CString(text), C.CString(kind), C.CString(ref)
		defer C.free(unsafe.Pointer(ct))
		defer C.free(unsafe.Pointer(cb))
		defer C.free(unsafe.Pointer(ck))
		defer C.free(unsafe.Pointer(cr))
		C.lsNotifySend(ct, cb, ck, cr)
	}
}

// notifyOnce is "LocalSites notify <title> <text>": a service started from the command line runs it so the
// notification shows LocalSites' name and icon. Exits non-zero when macOS didn't take it (the caller then
// falls back to osascript). A refusal of permission counts as handled: the person said no.
func notifyOnce(title, text, kind, ref string) int {
	if !inBundle() {
		return 1
	}
	ct, cb, ck, cr := C.CString(title), C.CString(text), C.CString(kind), C.CString(ref)
	defer C.free(unsafe.Pointer(ct))
	defer C.free(unsafe.Pointer(cb))
	defer C.free(unsafe.Pointer(ck))
	defer C.free(unsafe.Pointer(cr))
	switch C.lsNotifySendWait(ct, cb, ck, cr) {
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

// listenForNoticeButtons makes the app receive presses on notification buttons (see noticeAction).
func listenForNoticeButtons() {
	if inBundle() {
		C.lsNotifyListen()
	}
}

//export lsNoticeAction
func lsNoticeAction(kind, ref, action *C.char) {
	go noticeAction(C.GoString(kind), C.GoString(ref), C.GoString(action))
}
