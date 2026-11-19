//go:build darwin

package api

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <sys/socket.h>
#include <sys/un.h>
#include <bsm/libbsm.h>
#include <Security/Security.h>

#ifndef LOCAL_PEERTOKEN
#define LOCAL_PEERTOKEN 0x006
#endif

static SecRequirementRef lsSelfReq = NULL;

// lsSelfSigned: 1 when this program is signed with a certificate (not ad hoc), keeping its designated
// requirement for checking callers; 0 otherwise.
static int lsSelfSigned(void) {
	SecCodeRef self = NULL;
	SecStaticCodeRef st = NULL;
	SecRequirementRef req = NULL;
	CFStringRef text = NULL;
	int ok = 0;
	if (SecCodeCopySelf(kSecCSDefaultFlags, &self) != errSecSuccess) return 0;
	if (SecCodeCopyStaticCode(self, kSecCSDefaultFlags, &st) == errSecSuccess &&
	    SecCodeCopyDesignatedRequirement(st, kSecCSDefaultFlags, &req) == errSecSuccess &&
	    SecRequirementCopyString(req, kSecCSDefaultFlags, &text) == errSecSuccess) {
		CFRange r = CFStringFind(text, CFSTR("certificate leaf"), 0);
		if (r.location != kCFNotFound) { lsSelfReq = req; req = NULL; ok = 1; }
	}
	if (text) CFRelease(text);
	if (req) CFRelease(req);
	if (st) CFRelease(st);
	CFRelease(self);
	return ok;
}

// lsPeerIsSelf: 1 when the program on the other end of the unix socket fd is signed the same way as
// this one (same identifier and certificate), checked by the kernel's identity for that process.
static int lsPeerIsSelf(int fd) {
	if (!lsSelfReq) return 0;
	audit_token_t tok;
	socklen_t len = sizeof(tok);
	if (getsockopt(fd, SOL_LOCAL, LOCAL_PEERTOKEN, &tok, &len) != 0) return 0;
	CFDataRef data = CFDataCreate(NULL, (const UInt8 *)&tok, sizeof(tok));
	const void *keys[] = { kSecGuestAttributeAudit };
	const void *vals[] = { data };
	CFDictionaryRef attrs = CFDictionaryCreate(NULL, keys, vals, 1, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	SecCodeRef code = NULL;
	int ok = 0;
	if (SecCodeCopyGuestWithAttributes(NULL, attrs, kSecCSDefaultFlags, &code) == errSecSuccess) {
		ok = SecCodeCheckValidity(code, kSecCSDefaultFlags, lsSelfReq) == errSecSuccess;
		CFRelease(code);
	}
	CFRelease(attrs);
	CFRelease(data);
	return ok;
}
*/
import "C"

import (
	"net"
	"sync"
)

var (
	selfOnce   sync.Once
	selfSigned bool
)

// canProveApp: this service is signed with a certificate, so it can tell its own app from other
// programs by their signatures. Development builds (ad hoc or unsigned) can't.
func canProveApp() bool {
	selfOnce.Do(func() { selfSigned = C.lsSelfSigned() == 1 })
	return selfSigned
}

// peerIsSelf reports whether the program on the other end of c is LocalSites, signed like this service.
func peerIsSelf(c net.Conn) bool {
	uc, ok := c.(*net.UnixConn)
	if !ok || !canProveApp() {
		return false
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return false
	}
	res := false
	_ = raw.Control(func(fd uintptr) { res = C.lsPeerIsSelf(C.int(fd)) == 1 })
	return res
}
