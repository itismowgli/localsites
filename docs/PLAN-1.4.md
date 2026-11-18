# LocalSites 1.4: plan

Branch: `linux-editors`. Nothing is merged into `master` or released until every part below is done and the
full test gate passes. Then: merge, version 1.4.0, one release.

Goal: someone installs LocalSites on a new Mac or Linux computer and their sites work without a terminal.

Order of work:

1. Editor chooser (Mac and Linux share it)
2. `.test` domains and HTTPS without Valet (Mac)
3. Remove and rename projects
4. Check the MAMP and XAMPP importers against real files
5. Linux (see the Linux section for how PHP, MySQL and Apache are installed)
6. Release

---

## 1. Editor chooser

### What's wrong today

- The Open buttons use the first editor found, or VS Code when none is found, even if VS Code isn't installed.
  Nobody is asked.
- The choice lives in the app window's browser storage. The CLI and AI agents don't know it, so the command
  palette always opens VS Code.
- Finding apps only works on macOS.

### How it will work

- LocalSites lists the editors and terminals installed on the computer. Nothing is chosen until the person picks one.
- The first time someone clicks **Open in editor** (or terminal) without a choice, a small chooser opens:
  "Which editor do you want to use?" It lists the installed editors with their icons and a **Remember my choice**
  box (on by default). With the box on, the pick becomes the default; with it off, the project just opens once.
- If the saved editor has been uninstalled, the button says **Choose editor** and the chooser opens again with a note.
- Change it any time:
  - **Settings → General → Code editor / Terminal**: a dropdown of installed apps, plus "Ask every time".
  - **Open → More apps** menu: each editor has **Make default**.
  - The chooser itself.
- The choice is saved in LocalSites settings (`apps.editor`, `apps.terminal`), so the app, `localsites open` and
  the agents' `open` tool all use the same one. A choice saved in the old browser storage is moved over once.
- `localsites open <project>` with no app opens the chosen editor; `--app` picks another. If nothing is chosen,
  the CLI lists the installed editors and tells the person how to choose (`localsites open <project> --app cursor
  --remember`). Agents get the same list in the error, so they can ask the person.

### Editors and terminals found

Shared catalog with a Mac and a Linux way of finding each app.

| Kind | Apps |
|---|---|
| Editors | VS Code, VSCodium, Cursor, Windsurf, Zed, PhpStorm, WebStorm, IntelliJ IDEA, Sublime Text, Nova (Mac), BBEdit (Mac), Kiro, Antigravity, Rune |
| Terminals | Terminal (Mac), iTerm2 (Mac), Warp, Ghostty, kitty, Alacritty, WezTerm; on Linux also GNOME Terminal, Ptyxis, Konsole, Xfce Terminal, Tilix |

- **Mac:** the `.app` bundle in `/Applications`, `~/Applications` (JetBrains Toolbox puts apps there) or Setapp,
  opened with `open -a`.
- **Linux:** the app's `.desktop` file in the standard folders (`/usr/share/applications`,
  `~/.local/share/applications`, Flatpak and Snap export folders), or its command on `PATH` (`code`, `cursor`,
  `zed`/`zeditor`, `subl`, JetBrains Toolbox scripts). Opened by running the app's own command with the folder as
  an argument, never through a shell. Terminals use their "start in this folder" option (for example
  `gnome-terminal --working-directory=…`, `konsole --workdir …`, `kitty --directory …`).

### Tests

- Unit: finding apps on Mac and Linux from fake folders; parsing `.desktop` files (Exec field codes, Flatpak
  `flatpak run`); the command line built for each terminal; saving and moving the choice.
- UI: first click opens the chooser; Remember saves the default; Ask every time; uninstalled default; Make default
  from More apps.

---

## 2. `.test` domains and HTTPS without Valet (Mac)

### What's wrong today

On a Mac without Laravel Valet, LocalSites uses its own built-in proxy, which needs three things it can't do yet:

1. **Ports 80 and 443.** The proxy listens on `127.0.0.1:80`, which macOS only allows for the system
   (`Permission denied`, checked on a real Mac). So the proxy doesn't start.
2. **`.test` names.** macOS needs `/etc/resolver/test` to send `.test` lookups to LocalSites' DNS (port 5354).
3. **Certificate trust.** Browsers warn "Your connection is not private" until the LocalSites certificate
   authority is trusted. Today Doctor only shows a `sudo` command.

### How it will work

- **Ports:** macOS lets normal apps listen on ports below 1024 on *all* addresses, just not on `127.0.0.1` only.
  The proxy will listen on all addresses and close any connection that doesn't come from this Mac before reading
  a byte, so the network sees nothing. No password needed. If another app holds port 80 or 443 (MAMP PRO's
  Apache, Docker), Doctor names it.
- **One setup step with one password prompt**, shown in the same Setup card as Fix everything:
  "Set up .test domains". It writes `/etc/resolver/test` (only that file) and trusts the LocalSites certificate
  authority in the System keychain. The authority can only sign `.test` names, so trusting it can't be used to
  fake other websites.
- If macOS refuses the system-wide trust (some versions only accept trust changes the person confirms in its
  own dialog), LocalSites trusts the certificate for the person's account instead; macOS shows its own password
  dialog for that.
- Checked on this Mac: listening on a port below 1024 on all addresses works without a password, and the proxy
  accepts only connections from this computer. If the macOS firewall is turned on, it may ask once whether
  LocalSites may accept incoming connections; answering either way is fine, since only local connections are used.
- Doctor gets a **Repair** button on each of the three checks, using the same step.
- Valet users: nothing changes; Valet keeps serving `.test`.

### Tests

- Unit: the loopback-only listener refuses other addresses; the setup step writes the right file contents and
  passes paths safely; Doctor checks for each part.
- Manual, on a Mac without Valet (a new macOS user account works): click the step, enter the password, open a site
  with the padlock.

---

## 3. Remove and rename projects

Projects are folders inside the projects folders, so there are two kinds of "remove". Files are only ever moved,
never deleted.

- **Hide from LocalSites:** the folder stays where it is and LocalSites stops listing and serving it. Settings →
  Projects folders → Hidden projects can bring it back.
- **Move to Trash:** the folder goes to the Trash (Finder's Put Back works on Mac; on Linux the freedesktop Trash).
  The confirm step shows the folder path and size and asks the person to type the project name.
- **The project's database is never removed with it.** The confirm step names the database and says it stays; it
  can be deleted from Databases, which makes a backup first.
- **Rename:** renames the folder in the same projects folder. LocalSites stops the project, renames the folder,
  moves its settings and logs, and starts it again. The `.test` address follows the new name unless a custom address
  was set. The database keeps its name. Refused when the new name already exists, when Valet serves the project,
  or when the folder is a git worktree of another project.
- Agents can hide and rename only after the person clicks Approve, and can't move projects to the Trash at all.

### Tests

- Unit and e2e on sandbox folders: hide and unhide, rename with settings moved, refused cases, Trash.
- UI: the confirm steps.

---

## 4. MAMP and XAMPP importers

Installing free MAMP on this Mac is not safe: MAMP and MAMP PRO share `/Applications/MAMP`, and MAMP PRO's MySQL
holds real data. MAMP PRO's installer already puts free MAMP's files there, so the check used those files,
read-only.

**Found and fixed:**

- Free MAMP ships `httpd-vhosts.conf` with two Apache example hosts (`dummy-host.example.com`), and its
  `httpd.conf` doesn't load that file (the `Include` line is commented out). The importer read it anyway and
  offered the example hosts. XAMPP for macOS ships Apache's same stock files.
- Free MAMP and XAMPP users keep projects as folders in `htdocs`, which the importer never looked at.

Now the importer reads the main `httpd.conf`: it uses a virtual-host file only when `httpd.conf` includes it,
always skips Apache's example hosts, and lists the folders in `htdocs` with the PHP version MAMP's PHP module
uses. The Import screen offers to add `htdocs` as a projects folder when it has projects. MAMP PRO is unchanged
(checked on this Mac: the same 9 hosts and PHP versions).

**Not checked on a real install:** XAMPP's own files (its installer can't be unpacked without installing) and
copying a database from XAMPP's MariaDB. The tests use Apache's stock files with XAMPP's paths.

---

## 5. Linux

### Decision: Homebrew on Linux too

Three ways were compared: Homebrew on Linux, LocalSites' own downloads (static PHP builds and Oracle's MySQL
archive), and distro packages (apt/dnf with the Sury and Remi repositories). Homebrew came out best on coverage,
safety for MySQL data, and how much is shared with the Mac.

**Facts that decided it** (checked against the sources in November 2026):

- The shivammathur/php tap has ready-built Linux packages for **x86_64 and arm64** for every version, including
  the old ones (php@5.6, 7.1, 7.4, 8.0 checked). `mysql@8.4`, `httpd` and every add-on LocalSites offers (Mailpit,
  Redis, Valkey, Memcached, Meilisearch, PostgreSQL, MinIO) do too.
- Static PHP builds (static-php-cli) officially cover only PHP 8.2–8.5. The public downloads have no checksums
  and were four months behind on security fixes. LocalSites would have to build, sign and host PHP itself every
  month, and still couldn't cover PHP 7.x.
- Taking Homebrew's packages without Homebrew doesn't work: the programs have Homebrew's folder compiled in.
- Distro packages cover old PHP through the Sury and Remi repositories, but need three different repository
  setups, start system services that must be blocked, ship MySQL 8.0 (Ubuntu) or only MariaDB (Debian), and have
  no packages for Mailpit, Meilisearch or MinIO. Almost nothing would be shared with the Mac.
- A fresh Homebrew with PHP 8.3, MySQL 8.4 and Apache is about 410 MB to download and 1.3 GB on disk.

**So, on Linux:**

| Part | Source |
|---|---|
| PHP 5.6–8.5 | Homebrew (shivammathur/php tap), the same as the Mac |
| MySQL 8.4 | Homebrew `mysql@8.4`, pinned so it never jumps to a new major version; data in LocalSites' own folder |
| Apache, nginx | Homebrew `httpd`, `nginx`, run as the person with LocalSites' own config |
| Add-ons | Homebrew, unchanged |
| PHP already installed from Sury, Remi or the distro | Found and offered as extra choices. LocalSites never adds those repositories |

**How LocalSites installs Homebrew on Linux:** one password prompt (polkit `pkexec`, asked from the app window,
never from the background service) creates `/home/linuxbrew` for the person and installs the few system tools
Homebrew needs (`git curl file procps`, plus `build-essential` on Debian and Fedora). Then Homebrew's official
install script runs as the person, without a terminal, with progress in the Setup card. If someone already has
Homebrew, LocalSites uses it.

**Keeping PHP patched** (Mac and Linux):

- LocalSites checks weekly and offers "Update PHP, Apache and MySQL" with one click.
- It upgrades only the packages it uses (`brew upgrade <those>`), never everything, and never runs `brew cleanup`,
  so the previous version stays on disk.
- After upgrading it restarts its services and checks every site. If a site breaks, it switches back to the
  previous version and says what happened. Old versions are removed only after 14 days without problems.
- MySQL gets 8.4 patch releases only, always after an automatic backup.

**Rules that never bend:**

- LocalSites' MySQL uses only its own data folder and port. It never reads, changes, stops or upgrades the
  system's MySQL or MariaDB (`/var/lib/mysql`), and refuses to start a data folder made by a newer MySQL.
- It never removes, replaces or reconfigures system packages, the system Apache, PHP or their services.
- Homebrew never runs as root. The password prompt is asked once, shows what it will do, and when no prompt is
  possible (no desktop session) LocalSites shows the exact command to copy instead.

**What would change the decision:** shivammathur dropping Linux packages or old versions; Homebrew moving
Ubuntu LTS out of its top support tier; many breakages on Debian or Fedora; or someone shipping signed,
promptly updated PHP builds that include 7.x, which would make LocalSites' own downloads worth it on both
platforms. The code finds PHP, Apache and MySQL through one small "provider" layer, so a different source can
be added later without touching the rest.

### What the Linux version needs (from `docs/LINUX.md`, updated)

| Area | macOS | Linux |
|---|---|---|
| PHP, Apache, MySQL, add-ons | Homebrew | Homebrew at `/home/linuxbrew/.linuxbrew` |
| Installing Homebrew | official signed `.pkg` | one `pkexec` prompt, then the official install script as the person |
| Fixing root-owned files | macOS password prompt | the same `pkexec` prompt |
| Broken PHP check | missing `.dylib` | missing `.so` |
| Passwords | Keychain | Secret Service (GNOME Keyring, KWallet) over D-Bus; the file store when neither runs |
| `.test` names | `/etc/resolver/test` | systemd-resolved drop-in `/etc/systemd/resolved.conf.d/localsites.conf` |
| Ports 80 and 443 | listen on all addresses, refuse other computers | the same, with `cap_net_bind_service` set by the package (or the system's unprivileged-port setting) |
| Certificate trust | System keychain | system store (`update-ca-certificates` / `update-ca-trust`) and the Chrome/Firefox NSS stores |
| Start at login | launchd agent | systemd user unit |
| Notifications | native / `osascript` | `notify-send` |
| Opening apps | `open -a` | the app's command or `.desktop` file (see part 1) |
| Tray icon | menu bar | AppIndicator |
| App window | Wails (WebKit) | Wails (WebKitGTK 4.1) |
| Packages and updates | signed `.zip` of the app | `.deb` and `.rpm`, signed update manifest |

Targets: Ubuntu 24.04 LTS first, then Debian 12+ and Fedora; x86_64 and arm64.

See also `docs/LINUX.md` for how the other Mac-only parts map to Linux (passwords, DNS, ports, certificates,
start at login, notifications, tray icon, packages).

### Testing Linux

- GitHub Actions runs unit tests and builds on `ubuntu-latest`.
- End-to-end tests run in throwaway Docker containers for Ubuntu 24.04, Debian 12 and Fedora (x86_64), and on this
  Mac in arm64 containers.
- The desktop parts (window, tray icon, `.test` domains, certificate trust) need a real Linux desktop. Plan: an
  Ubuntu 24.04 desktop in a virtual machine on this Mac (UTM). The CoreX server runs live services, so nothing is
  installed on it directly; it may run test containers only if the owner agrees.

---

## 6. Release

- README, website, roadmap and `docs/PLAN.md` updated.
- Changelog entry for 1.4.0.
- Full gate: `make lint`, `go test ./...`, e2e, UI tests, Linux CI.
- Merge `linux-editors` into `phase-1` and `master`, tag `v1.4.0`, publish the release.
