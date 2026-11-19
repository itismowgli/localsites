# Design system

One set of tokens and components for every screen, so a new service or feature looks and behaves like the
rest of the app. The style is dark-first, dense, quiet and keyboard-first.

Code: tokens and `c-*` component classes at the end of `desktop/frontend/dist/styles.css`; the `UI.*`
helpers near the top of `desktop/frontend/dist/app.js`. Build screens from these. If something is missing,
add it here as a component, not as one-off CSS in a view.

## Tokens

The app and the website share one set of tokens: `design/tokens.json`. `node tools/build-tokens.mjs` turns it
into `tokens.css` for both (`desktop/frontend/dist/tokens.css`, `site/tokens.css`); `make lint` fails when they
are stale, when a text colour falls under 4.5:1 on any surface, or when a stylesheet uses a raw colour.

Three layers:

1. **Primitives**: raw values. Grays are pure neutrals named by OKLCH lightness × 1000 (`--gray-140` is
   lightness 0.14), plus blue (links, charts), amber (charts, warnings), green/red (status), eight git-lane
   hues and Valet's red.
   Components never use primitives directly.
2. **Themes**: roles mapped to primitives, for dark and light. `default` is the only theme today. Roles:
   `background` `window` `card` `popover` `muted` `hover` `accent` (selected-row wash) · `foreground`
   `text-secondary` `muted-foreground` `text-faint` (four text roles) · `primary` / `primary-foreground` (the
   main action: white on black in dark, black on white in light) · `secondary` · `brand` / `brand-foreground` (blue:
   links, badges, selection) · `border` `border-subtle` `input` `field` `ring` · `success`
   `warning` `danger` · `scrim` · `chart-1…5` · `lane-1…8` · `valet` `valet-text`.
3. **Scales**: `--space-1…16` (4 px grid), `--radius-sm` 4 · `--radius-md` 6 · `--radius` 8 (controls) ·
   `--radius-xl` 12 (cards, panels), `--font-sans` (Inter) and `--font-mono` (the system monospace), `--text-xs…4xl`,
   `--ease`, `--duration-fast` 120 ms, `--duration` 200 ms, `--shadow-sm` and `--shadow-overlay` (overlays
   only: depth comes from surface steps and hairlines).

The app keeps short aliases for the roles at the top of `styles.css` (`--panel`, `--line`, `--text-2`…), and
its own sizing tokens (`--s-*` spacing, `--fs-*` type, `--h-*` heights) near the end.

### Adding a theme, and customising

- **A new theme** is one more entry under `themes` with every role for dark and light (the build refuses a
  theme with a missing role or unreadable text). It's switched on with `data-palette="<name>"` on `<html>`;
  `data-theme="light|dark"` still picks the mode.
- **A person's own tweak** (say, a different brand colour) is an inline variable on `<html>`, like
  `style="--brand: …; --primary: …"`: every component follows, because components only read roles.
- Never hard-code a colour in a view or a stylesheet: if a role is missing, add it to the tokens.

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
- **Readable text:** every text role (`--text`, `--text-2`, `--muted-foreground`, `--faint`) reaches 4.5:1 on every surface it sits on, including `--active` rows, in both themes. Use `--faint` for the quietest text, never lower opacity. Scope rules for small parts (counts, hints) narrowly so they never recolour buttons or badges nearby (a `.vtabs button span` rule once turned the Filter badge grey).
- **Same order everywhere:** status → main action → secondary actions → icon actions (connect, options, log).
- **Responsive by region width**, not window width: the size observer in app.js adds `lt-<region>-<px>` classes
  (`panel`, `view`, `pm`) when a breakpoint is crossed; style with `.lt-view-720 .row …`. Don't use CSS container
  queries: WebKit re-checks them on every frame of a window resize, which made zooming lag (7 ms → 1 ms per frame
  after removing them). `make lint` rejects them. Cards wrap at 300 px; form fields stack under 600 px.
- **Accessibility:** every icon-only button has a title (becomes its accessible name); focus ring stays visible.

## Website (public pages)

These apply to `site/` only; the app keeps its own rules above.

- **Buttons are fully rounded** (`border-radius: 999px`). The app's buttons keep the 6px control radius.
- **Product screenshots use the full container width** (the `.wrap`, 1120px), with no scale or scroll effects.
- **Nested corners line up**: an image's radius is its frame's radius minus the frame's border and padding
  (hero 22 → 13, section screenshots 16 → 9). No fades or gradients over screenshots.
- **Figures sit only beside section headings** (Hairline, `site/vendor/hairline.js`), one per section, matching
  what the section is about, with no read-out text. Feature items use a line icon in a 40px framed square (Lucide,
  1.5 stroke, from `site/icons.svg`, built by `tools/build-site-icons.mjs`); the item figures they replaced are
  kept as comments in the page.
- **Feature lists fill their grid evenly**: six items in three columns, four in four. Each item is an icon, a `<b>`
  title and a `<span class="pt-d">` description; a subgrid lines up titles and descriptions across the row. Descriptions are one idea,
  55 to 80 characters, and every public text goes through the humanizer before it's written.
- **Text uses the type styles only** (`font: var(--type-…)`), in Inter: display 48/1.1 semibold (hero, big
  numbers), headline-lg 36/1.15 semibold (section titles), headline-md 24/1.25 medium, body-lg 18/1.6 (leads, item
  titles in medium), body-md 16/1.6 (body), body-sm 14/1.5 (notes, tables, footer), label-md 14/1.4 medium (buttons,
  nav, column heads), mono 13/1.5. On phones the display steps down to headline-lg. `make lint` rejects a typed size.
- **Spacing uses the space steps only**: `--space-1` 4, `--space-2` 8, `--space-4` 16, `--space-6` 24, `--space-8` 32,
  `--space-12` 48, `--space-16` 64, and multiples of `--space-section` (64) between sections. 1 to 3px nudges inside
  small controls are fine. `make lint` rejects any other typed padding, margin or gap, inline styles included.
- Colours come from `tokens.css`, the same tokens as the app.

