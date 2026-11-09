#import <Cocoa/Cocoa.h>

extern void lsWindowVisible(int);

// Every 2 s (cheap, on the main thread) check whether the app window is on screen: closed to the
// menu bar, minimized or hidden with ⌘H count as not visible. A window merely covered by other
// windows still counts as visible, so switching back to LocalSites never reloads it. Only changes
// are reported.
void lsWatchWindow(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        __block int last = -1;
        NSTimer *t = [NSTimer timerWithTimeInterval:2.0 repeats:YES block:^(NSTimer *timer) {
            Class main = NSClassFromString(@"WailsWindow");
            int visible = 0;
            for (NSWindow *w in NSApp.windows) {
                if (main && ![w isKindOfClass:main]) continue;
                if (w.isVisible && !w.isMiniaturized && !NSApp.isHidden) visible = 1;
            }
            if (visible != last) {
                last = visible;
                lsWindowVisible(visible);
            }
        }];
        t.tolerance = 1.0; // let macOS group the wake-up with others
        [[NSRunLoop mainRunLoop] addTimer:t forMode:NSRunLoopCommonModes];
    });
}
