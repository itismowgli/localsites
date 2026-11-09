# Design system

One set of tokens and components for every screen, so a new service or feature looks and behaves like the
rest of the app. The style is dark-first, dense, quiet and keyboard-first.

Code: tokens and `c-*` component classes at the end of `desktop/frontend/dist/styles.css`; the `UI.*`
helpers near the top of `desktop/frontend/dist/app.js`. Build screens from these. If something is missing,
add it here as a component, not as one-off CSS in a view.

## Tokens

| Token | Values | Use |
|---|---|---|
| Spacing | `--s-1` 4 · `--s-2` 8 · `--s-3` 12 · `--s-4` 16 · `--s-5` 20 · `--s-6` 24 · `--s-8` 32 · `--s-10` 40 | all gaps and padding (4 px grid) |
| Type | `--fs-xs` 11.5 · `--fs-sm` 12.5 · `--fs-md` 13.5 (body) · `--fs-lg` 15 · `--fs-xl` 22 | Inter; one weight step for emphasis (500 → 600) |
| Radius | `--r` 6 (controls) · `--r-lg` 8 (cards, menus) · `--r-xl` 12 | |
| Height | `--h-ctl` 30 · `--h-ctl-sm` 26 · `--h-row` 42 | buttons, inputs, list rows |
| Motion | `--dur-fast` 120 ms · `--dur` 180 ms, `--ease` | transform and opacity only; off with Reduce Motion and while the window resizes |
| Colour | `--win` `--panel` `--raised` `--hover` `--active` · `--line` `--line-soft` · `--text` `--text-2` `--muted` `--faint` · `--accent` · `--green` `--yellow` `--orange` `--red` | never hard-code colours in views; light and dark come from the same tokens |

## Components

| Component | Helper | When |
|---|---|---|
| Page | `.c-page` | settings-like screens (Services): padding and max width |
| Section header | `UI.section({title, note, right})` | every group on a page: a title, one plain sentence, actions on the right |
| Card | `UI.card({ic, title, tags, sub, status, body, foot, on})` | anything you can install, switch on or connect (add-ons, integrations, tunnels) |
| Status pill | `UI.status(state, label)` | the only way to show state: `on` Running, `off` Off/Stopped, `warn` Starting/attention, `err` problem, `busy` working |
| Tag | `UI.tag(text)` | licence, version, small facts |
| Key/value | `UI.kv(label, html)` | short facts inside cards and panels |
| Buttons | `UI.btn(label, {ic, kind, sm})`, `UI.iconBtn(ic, title)` | `primary` for the one main action, default for others, `ghost` for quiet ones, `danger` for destructive |
| Switch | `UI.toggle(on, attrs, label)` | on/off that applies immediately |
| Tabs | `UI.tabs(items, active, attr)` | switching between variants of the same content (Laravel / Node / Django …) |
| Code block | `UI.code(text)` | anything the user copies; always has a Copy button |
| Form field | `UI.field(label, control, hint)` | dialogs; the hint says what happens, in one line |
| Empty state | `emptyState({...})` + `startCards()` | when a list has nothing yet: what it is, the main action, and a few starting points |

## Rules

- **Words:** short, plain sentences. Say what happens ("Emails your projects send stay here"), not what a
  feature is called. No jargon where a common word works. Run new copy through the Humanizer.
- **One primary action per card or dialog.** Destructive actions ask first and say exactly what is lost.
- **Installs and changes show the command first** (`brew install mailpit`), then run in the background with a log.
- **State is never colour alone:** the status pill always has a word.
- **Readable text:** every text colour token (`--text`, `--text-2`, `--muted`, `--faint`) reaches 4.5:1 on every surface it sits on, including `--active` rows, in both themes. Use `--faint` for the quietest text, never lower opacity. Scope rules for small parts (counts, hints) narrowly so they never recolour buttons or badges nearby (a `.vtabs button span` rule once turned the Filter badge grey).
- **Same order everywhere:** status → main action → secondary actions → icon actions (connect, options, log).
- **Responsive by region width**, not window width: the size observer in app.js adds `lt-<region>-<px>` classes
  (`panel`, `view`, `pm`) when a breakpoint is crossed; style with `.lt-view-720 .row …`. Don't use CSS container
  queries: WebKit re-checks them on every frame of a window resize, which made zooming lag (7 ms → 1 ms per frame
  after removing them). `make lint` rejects them. Cards wrap at 300 px; form fields stack under 600 px.
- **Accessibility:** every icon-only button has a title (becomes its accessible name); focus ring stays visible.
