//go:build darwin

// Menu bar icon (NSStatusItem). Called from tray_darwin.go.
#import <Cocoa/Cocoa.h>
#include <stdlib.h>

extern void lsTrayClicked(char *id);

@interface LSTrayTarget : NSObject
- (void)clicked:(NSMenuItem *)sender;
@end
@implementation LSTrayTarget
- (void)clicked:(NSMenuItem *)sender {
	NSString *ident = sender.representedObject;
	if (ident) lsTrayClicked((char *)[ident UTF8String]);
}
@end

static NSStatusItem *gItem = nil;
static LSTrayTarget *gTarget = nil;

void lsTrayInit(const void *iconData, int iconLen) {
	NSData *data = iconLen > 0 ? [NSData dataWithBytes:iconData length:iconLen] : nil;
	dispatch_async(dispatch_get_main_queue(), ^{
		if (gItem != nil) return;
		gTarget = [LSTrayTarget new];
		gItem = [[NSStatusBar systemStatusBar] statusItemWithLength:NSVariableStatusItemLength];
		NSImage *img = data ? [[NSImage alloc] initWithData:data] : nil; // the LocalSites logo, 18 pt
		if (img) img.size = NSMakeSize(18, 18);
		else img = [NSImage imageWithSystemSymbolName:@"server.rack" accessibilityDescription:@"LocalSites"];
		img.template = YES;
		gItem.button.image = img;
		gItem.button.toolTip = @"LocalSites";
		gItem.menu = [NSMenu new];
	});
}

// lsTraySetMenu rebuilds the menu from lines "kind\tid\ttitle" (kind: item | header | sep | disabled).
void lsTraySetMenu(const char *spec) {
	NSString *s = [NSString stringWithUTF8String:spec];
	dispatch_async(dispatch_get_main_queue(), ^{
		if (gItem == nil) return;
		NSMenu *m = [NSMenu new];
		m.autoenablesItems = NO;
		for (NSString *line in [s componentsSeparatedByString:@"\n"]) {
			NSArray *f = [line componentsSeparatedByString:@"\t"];
			if (f.count < 1 || [f[0] length] == 0) continue;
			if ([f[0] isEqualToString:@"sep"]) { [m addItem:[NSMenuItem separatorItem]]; continue; }
			if (f.count < 3) continue;
			NSMenuItem *it = [[NSMenuItem alloc] initWithTitle:f[2] action:@selector(clicked:) keyEquivalent:@""];
			it.target = gTarget;
			it.representedObject = f[1];
			if ([f[0] isEqualToString:@"header"] || [f[0] isEqualToString:@"disabled"]) it.enabled = NO;
			[m addItem:it];
		}
		gItem.menu = m;
	});
}

// Dock icon badge, like Slack's unread count: things that need the person (empty clears it).
void lsSetBadge(const char *label) {
	NSString *s = [NSString stringWithUTF8String:label];
	dispatch_async(dispatch_get_main_queue(), ^{
		[[NSApp dockTile] setBadgeLabel:(s.length ? s : nil)];
	});
}
