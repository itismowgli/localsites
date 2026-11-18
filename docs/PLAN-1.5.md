# LocalSites 1.5: plan

Branch: `notifications`. Merged and released only when every part below is done and the full test gate passes.

This is phase B of `docs/ROADMAP-2.md` (3.1 and 3.2), plus the 1.4 leftovers listed at the end.

## What's wrong today

- The service sends a system notification for a crash, a finished install, an update, a problem and an agent
  request waiting for approval. Nothing keeps them: a notification missed while away is gone.
- There's no way to turn a kind off or to pause them.
- They have no buttons. To approve an agent request you have to find the app window.

## 1. Notification center (ROADMAP-2 3.1)

### What's kept

Every notification is also saved in a list (`<data>/notices.json`, the newest 200, at most 30 days):

| Kind | When | Opens |
|---|---|---|
| `agent` | an AI agent or script waits for approval | the request card |
| `crash` | a service stopped unexpectedly and was restarted, or keeps crashing | Logs for that service |
| `install` | a background install finished (dependencies, add-on, tunnel client) | the project or add-on |
| `setup` | Fix everything or Install Homebrew finished | Overview |
| `share` | a share ends in 5 minutes, or has ended | the project's Share section |
| `update` | an update is available or ready | Settings → Updates |
| `problem` | a new problem was recorded | Settings → Problems |

### In the app

- A **bell** in the status bar with the number of unread notifications. It opens a panel: newest first, grouped
  by day, each with its time and a click that opens the right place. **Mark all as read** and **Clear**.
- **Do Not Disturb** from the bell: for 1 hour, until tomorrow morning, or until turned off. While it's on,
  nothing pops up on the screen, but everything still lands in the list.
- **Settings → Notifications**: one switch per kind for the system notification. The list always keeps
  everything; agent requests can't be switched off, since an agent would wait without anyone knowing.

### For the CLI and agents

`notices.list` (with the unread count), `notices.read` (one or all), `notices.clear`, `notices.quiet`
(Do Not Disturb). `localsites notices` prints the list.

## 2. Action buttons on macOS notifications (ROADMAP-2 3.2)

| Kind | Buttons |
|---|---|
| `agent` | **Approve**, **Deny** |
| `share` (ending soon) | **Extend 30 min**, **Stop** |
| `crash` | **Open logs** |

- The app registers these as notification categories when it starts, and handles the button press: it calls
  the service as the app itself, exactly as if the button in the window had been pressed. If the app isn't
  running, macOS starts it.
- Clicking the notification itself opens the app at the right place.
- Linux keeps plain notifications for now (the desktop's action support varies too much to rely on).

## 3. Tests

- Unit: the store (limits, read state, saving), kinds switched off, Do Not Disturb, share-ending timer.
- End-to-end: a crashed service and an agent request appear in the list; quiet hours keep the list but send
  nothing.
- UI: bell count, panel, mark read, clear, Do Not Disturb, the settings switches, opening a notice.
- The macOS buttons need a real notification, so they're checked by hand on this Mac before release.

## 4. Left over from 1.4 (after phase B)

- Linux tray icon (StatusNotifierItem), in-app updates on Linux (download the package, install with one
  password prompt), signed `.deb`/`.rpm`.
