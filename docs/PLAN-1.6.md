# LocalSites 1.6: plan

Branch: `v1.6`. Released when every part below is done and the full test gate passes.

## 1. Sharing without installing anything (done)

tunnl.gg forwards a port over plain `ssh`, which comes with macOS and Linux. It is the default tunnel when the
Cloudflare client isn't installed. LocalSites runs ssh without the person's ssh settings, keys or agent, and
keeps tunnl.gg's host key in its own file. tunnl.gg sits behind Cloudflare, which overwrites forged visitor
addresses, so wrong passwords are limited per visitor. Visitors see tunnl.gg's safety notice once a day.

## 2. Agent safety

### What was wrong

- Some actions ran without asking: copying a database from another tool (`import.database`), editing other
  agent apps' settings (`agents.connect`, `agents.disconnect`), taking over a Valet site (`sites.takeover`),
  installing a project's dependencies, whose install scripts run code (`sites.install`), installing Homebrew
  software (`addons.install`), and switching on Adminer, which can reach every database (`tools.adminer`).
- The service believed whatever name a caller gave: a script could say it was the app and approve its own
  requests. The "typed in a terminal" check for the CLI can be faked by giving a program a pretend terminal.

### Changes

1. **Those actions ask first.** Adminer asks only when it's being switched on.
2. **The app proves who it is (macOS).** The service checks the code signature of the program on the other end
   of its socket, using the identity macOS attaches to the connection. Only LocalSites itself, signed with the
   same certificate as the service, counts as the app. Releases also turn on the hardened runtime, so no
   other program can load code into LocalSites.
3. **The terminal asks too, when the app can prove itself.** Risky commands typed in a terminal wait for
   Approve in the app or on the notification, like an agent's. Without that proof (Linux, development builds)
   the terminal check stays as before, so a headless Linux machine still works from the terminal.
4. **Fewer prompts for work you're watching.** The request card names the agent (from its MCP hello) and can
   approve the same action, by the same agent, on the same project for 10 minutes. Deleting a database or a
   project, installing updates and the actions in 1 never get this: they ask every time.

### Limits, said plainly

- On Linux there's no app signature to check, so any program running as you can still act as the app.
- An agent's name is what the agent says it is. A 10-minute approval is only as narrow as that name, the
  action and the project.

## 3. Website

The page copy is rewritten to read like a product, not a feature dump: one idea per section, short
sentences, no stacked claims. Section graphics are drawn with Hairline (interactive line figures).

## Tests

- Unit: which actions ask; a forged "app" name is refused; the terminal asks when the app is proven; 10-minute
  approvals match only the same agent, action and project, and never for the always-ask actions.
- macOS: a real signed build approves from the app, and the same request from a script is refused.
- tunnl.gg: a live share with a password (opt-in test, needs the internet).
