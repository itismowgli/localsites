//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>
#include <stdlib.h>
#include <string.h>

// Native drag regions (like Electron's): the page reports where its drag areas are and where the
// controls inside them are. A mouse-down on a drag area starts a real window drag from that same
// event, so macOS treats it like a title-bar drag (window tiling, snapping, Mission Control).

static double *gDrag = NULL; static int gNDrag = 0;
static double *gNo = NULL;   static int gNNo = 0;
static NSLock *gLock = nil;

static int inRects(double *r, int n, double x, double y) {
	for (int i = 0; i < n; i++) {
		double *q = r + i * 4;
		if (x >= q[0] && x < q[0] + q[2] && y >= q[1] && y < q[1] + q[3]) return 1;
	}
	return 0;
}

static double *copyRects(double *src, int n) {
	if (n <= 0) return NULL;
	double *d = malloc(sizeof(double) * 4 * n);
	memcpy(d, src, sizeof(double) * 4 * n);
	return d;
}

void lsSetRegions(double *drag, int nd, double *no, int nn) {
	if (gLock == nil) return;
	[gLock lock];
	free(gDrag); free(gNo);
	gDrag = copyRects(drag, nd); gNDrag = nd;
	gNo = copyRects(no, nn); gNNo = nn;
	[gLock unlock];
}

void lsInstallDrag(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		if (gLock != nil) return;
		gLock = [NSLock new];
		[NSEvent addLocalMonitorForEventsMatchingMask:NSEventMaskLeftMouseDown handler:^NSEvent *(NSEvent *e) {
			NSWindow *w = e.window;
			if (w == nil || ![w.title isEqualToString:@"LocalSites"] || (w.styleMask & NSWindowStyleMaskFullScreen)) return e;
			NSPoint p = e.locationInWindow;
			double x = p.x, y = w.contentView.frame.size.height - p.y; // page coordinates (top-left origin)
			[gLock lock];
			int hit = inRects(gDrag, gNDrag, x, y) && !inRects(gNo, gNNo, x, y);
			[gLock unlock];
			if (!hit) return e;
			if (e.clickCount == 2) { // same as a title bar: follows System Settings → Desktop & Dock
				NSString *a = [[NSUserDefaults standardUserDefaults] stringForKey:@"AppleActionOnDoubleClick"];
				if ([a isEqualToString:@"Minimize"]) [w performMiniaturize:nil];
				else if (![a isEqualToString:@"None"]) [w performZoom:nil];
				return nil;
			}
			[w performWindowDragWithEvent:e];
			return nil;
		}];
	});
}
*/
import "C"

import "unsafe"

func installNativeDrag() { C.lsInstallDrag() }

// SetDragRegions receives the page's drag areas and the controls inside them, as [x, y, width, height]
// in page points.
func (a *App) SetDragRegions(drag, noDrag [][4]float64) {
	flat := func(r [][4]float64) []C.double {
		out := make([]C.double, 0, len(r)*4)
		for _, q := range r {
			out = append(out, C.double(q[0]), C.double(q[1]), C.double(q[2]), C.double(q[3]))
		}
		return out
	}
	d, n := flat(drag), flat(noDrag)
	var dp, np *C.double
	if len(d) > 0 {
		dp = (*C.double)(unsafe.Pointer(&d[0]))
	}
	if len(n) > 0 {
		np = (*C.double)(unsafe.Pointer(&n[0]))
	}
	C.lsSetRegions(dp, C.int(len(drag)), np, C.int(len(noDrag)))
}
