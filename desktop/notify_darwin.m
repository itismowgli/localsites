// Notifications for the macOS app: permission, buttons (categories) and the delegate that receives presses.
// Kept out of notify_darwin.go: cgo copies a Go file's C preamble when it has //export, which would define
// the delegate class twice.
#import <Foundation/Foundation.h>
#import <UserNotifications/UserNotifications.h>

// Asks once for permission to show notifications. macOS only prompts the first time.
void lsNotifyAuthorize(void) {
	[[UNUserNotificationCenter currentNotificationCenter]
		requestAuthorizationWithOptions:(UNAuthorizationOptionAlert | UNAuthorizationOptionSound | UNAuthorizationOptionBadge)
		completionHandler:^(BOOL granted, NSError *error) {}];
}

// Buttons on notifications, by kind (the categoryIdentifier). Registered once when the app starts.
void lsNotifyCategories(void) {
	UNNotificationAction *approve = [UNNotificationAction actionWithIdentifier:@"approve" title:@"Approve"
		options:UNNotificationActionOptionAuthenticationRequired];
	UNNotificationAction *deny = [UNNotificationAction actionWithIdentifier:@"deny" title:@"Deny"
		options:UNNotificationActionOptionDestructive];
	UNNotificationAction *extend = [UNNotificationAction actionWithIdentifier:@"extend" title:@"Extend 30 min" options:0];
	UNNotificationAction *stop = [UNNotificationAction actionWithIdentifier:@"stop" title:@"Stop sharing"
		options:UNNotificationActionOptionDestructive];
	UNNotificationAction *logs = [UNNotificationAction actionWithIdentifier:@"logs" title:@"Open logs"
		options:UNNotificationActionOptionForeground];
	NSSet *cats = [NSSet setWithObjects:
		[UNNotificationCategory categoryWithIdentifier:@"agent" actions:@[approve, deny] intentIdentifiers:@[] options:0],
		[UNNotificationCategory categoryWithIdentifier:@"share" actions:@[extend, stop] intentIdentifiers:@[] options:0],
		[UNNotificationCategory categoryWithIdentifier:@"crash" actions:@[logs] intentIdentifiers:@[] options:0], nil];
	[[UNUserNotificationCenter currentNotificationCenter] setNotificationCategories:cats];
}

static UNMutableNotificationContent *lsContent(NSString *t, NSString *b, NSString *kind, NSString *ref) {
	UNMutableNotificationContent *content = [[UNMutableNotificationContent alloc] init];
	content.title = t;
	content.body = b;
	content.userInfo = @{@"kind": kind ?: @"", @"ref": ref ?: @""};
	// buttons only when there's something to act on
	if (ref.length > 0 && ([kind isEqualToString:@"agent"] || [kind isEqualToString:@"share"])) content.categoryIdentifier = kind;
	if ([kind isEqualToString:@"crash"]) content.categoryIdentifier = kind;
	return content;
}

extern void lsNoticeAction(char *kind, char *ref, char *action);

@interface LSNoticeDelegate : NSObject <UNUserNotificationCenterDelegate>
@end
@implementation LSNoticeDelegate
// While LocalSites is in front, still show the banner (it isn't only for when the app is hidden)
- (void)userNotificationCenter:(UNUserNotificationCenter *)center willPresentNotification:(UNNotification *)n
		withCompletionHandler:(void (^)(UNNotificationPresentationOptions))done {
	done(UNNotificationPresentationOptionBanner | UNNotificationPresentationOptionList | UNNotificationPresentationOptionSound);
}
// A button or the notification itself was clicked: pass it to Go
- (void)userNotificationCenter:(UNUserNotificationCenter *)center didReceiveNotificationResponse:(UNNotificationResponse *)r
		withCompletionHandler:(void (^)(void))done {
	NSDictionary *info = r.notification.request.content.userInfo;
	NSString *action = r.actionIdentifier;
	if ([action isEqualToString:UNNotificationDefaultActionIdentifier]) action = @"open";
	else if ([action isEqualToString:UNNotificationDismissActionIdentifier]) action = @"dismiss";
	lsNoticeAction((char *)[info[@"kind"] ?: @"" UTF8String], (char *)[info[@"ref"] ?: @"" UTF8String], (char *)[action UTF8String]);
	done();
}
@end

static LSNoticeDelegate *lsDelegate;

// lsNotifyListen makes this process receive button presses. Called early: macOS may start the app because
// of a press, and the press is delivered right after launch.
void lsNotifyListen(void) {
	lsDelegate = [[LSNoticeDelegate alloc] init];
	[UNUserNotificationCenter currentNotificationCenter].delegate = lsDelegate;
	lsNotifyCategories();
}

// lsNotifySendWait sends one notification and waits (up to 5 s) until macOS has it, for the short-lived
// "LocalSites notify" helper process. Returns 0 when it was handed over.
int lsNotifySendWait(const char *title, const char *body, const char *kind, const char *ref) {
	NSString *t = [NSString stringWithUTF8String:title];
	NSString *b = [NSString stringWithUTF8String:body];
	NSString *k = [NSString stringWithUTF8String:kind];
	NSString *rf = [NSString stringWithUTF8String:ref];
	UNUserNotificationCenter *center = [UNUserNotificationCenter currentNotificationCenter];
	lsNotifyCategories(); // the categories are per app: the helper registers them too
	dispatch_semaphore_t done = dispatch_semaphore_create(0);
	__block int result = 1;
	[center requestAuthorizationWithOptions:(UNAuthorizationOptionAlert | UNAuthorizationOptionSound | UNAuthorizationOptionBadge)
		completionHandler:^(BOOL granted, NSError *error) {
			if (!granted) { result = 2; dispatch_semaphore_signal(done); return; }
			UNNotificationRequest *req = [UNNotificationRequest requestWithIdentifier:[[NSUUID UUID] UUIDString]
				content:lsContent(t, b, k, rf) trigger:nil];
			[center addNotificationRequest:req withCompletionHandler:^(NSError *err) {
				result = err == nil ? 0 : 1;
				dispatch_semaphore_signal(done);
			}];
		}];
	dispatch_semaphore_wait(done, dispatch_time(DISPATCH_TIME_NOW, 5 * NSEC_PER_SEC));
	return result;
}

void lsNotifySend(const char *title, const char *body, const char *kind, const char *ref) {
	NSString *t = [NSString stringWithUTF8String:title];
	NSString *b = [NSString stringWithUTF8String:body];
	NSString *k = [NSString stringWithUTF8String:kind];
	NSString *rf = [NSString stringWithUTF8String:ref];
	UNUserNotificationCenter *center = [UNUserNotificationCenter currentNotificationCenter];
	[center requestAuthorizationWithOptions:(UNAuthorizationOptionAlert | UNAuthorizationOptionSound | UNAuthorizationOptionBadge)
		completionHandler:^(BOOL granted, NSError *error) {
			if (!granted) return;
			UNNotificationRequest *req = [UNNotificationRequest requestWithIdentifier:[[NSUUID UUID] UUIDString]
				content:lsContent(t, b, k, rf) trigger:nil];
			[center addNotificationRequest:req withCompletionHandler:nil];
		}];
}
