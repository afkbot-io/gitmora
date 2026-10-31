# Gitmora interface contract

The workspace serves engineers who need to find the exact task, agent session, commit and MR behind a change. Search and original evidence take priority; settings are secondary.

Mutation forms keep a normal disabled fieldset and apply their supplied layout to that fieldset. Base resets remove native border/padding; utility classes supply the intended panel/grid. Do not use `display: contents` on the fieldset: WebKit can retain inherited dark label colors after a theme switch. The actual iPhone settings matrix covers this regression. Hosted account pages must supply exactly one device-width viewport; desktop resizing alone does not verify phone rendering.

Palette: ink `#172B46`, canvas `#F5F7FB`, paper `#FFFFFF`, accent `#335CDE`, boundary `#DEE5F0`, muted text `#52647C`. Semantic danger/success colors indicate real outcomes only. IBM Plex Sans carries Russian/English interface prose; IBM Plex Mono is used for source, commands and hashes. Fonts ship locally.

Left-aligned project rail, query/results surface, exact evidence inspector. Mobile stacks the inspector below results and wraps navigation without horizontal overflow. Login and registration reuse the same foundations; a developer should recognize the same account through browser login and CLI consent.

```text
Project rail | Search / sessions / tasks / MR | Exact source
             | Repositories / integrations  | Commit and task links
             | Team / access / account       |
```

Review of the plan: keep the useful repository structure and restrained palette, remove ornamental product claims and advanced AI panels, expose the actual engineering artifacts. Do not add metric cards without real data. Login should identify Gitmora directly; the identity engine is an implementation detail.

Tailwind owns layout and styles. Shared tokens and typed variants define controls, alerts, panels and navigation. All active screens use the same focus, spacing, typography and disabled states. Preserve semantic HTML, accessible names, password autocomplete, keyboard order and reduced motion. Errors explain recovery; empty lists give a permitted next step. Validate real login/registration/consent and workspace flows at desktop and 390px widths with screenshots.


## Shared themes and rhythm

Both `light` and `dark` use the same semantic tokens: canvas, paper, ink, muted, line, control, accent/action, danger and success. Light remains white/cool grey with blue actions; dark uses navy surfaces, pale text and an independently contrast-checked action fill. Status colors convey actual state. Inputs, dialogs, tables, code/evidence panels and native account controls inherit these tokens; hard-coded white surfaces are excluded from active components.

Spacing follows Tailwind's 4px scale (8/12/16/24/32/40/56px by purpose). Primary controls have a 44px minimum height; compact text actions retain a 32px target; form panels use 24px padding and 16px on mobile. The rail is 240px (224px compact); the content uses 40px desktop / 24px compact / 16px mobile gutters. Typography uses a shared 12/14/16/18/20/24/30px scale. Body/source text keeps bounded line lengths; identifiers and long external content wrap without widening the viewport. Shared `buttonStyles` and `sharedStyles` own action, heading, form-grid, notice and panel primitives.

The same tiny external bootstrap runs before React or account styles, under the existing CSP. It restores the validated `gitmora.theme` preference, defaults to the system, works when storage is blocked and synchronizes other tabs/system changes without overriding an explicit choice. Credentials never enter storage. React subscribes through `useSyncExternalStore`; the hosted FreeMarker adapter uses the same runtime, tokens, labels and control styles. `color-scheme` and browser chrome follow the selection.

Navigation uses real fragment links with browser back/forward and modifier-click behavior. Mobile discloses the section list on demand and returns focus to content after selection. The project selector keeps an accessible label; unavailable project controls and redundant badges, slogans and the oversized idle-search placeholder are removed. Source selection survives hover; dialogs retain native focus trapping/Escape and cancellation focus.

Appearance is selected only in personal settings; the public presentation, hosted forms, workspace header and CLI consent have no theme control. `locale.js` independently restores the validated `gitmora.locale` preference. Authored messages are translated explicitly; source/user data is preserved. The provider uses its server-rendered locale and the application follows it after authentication.

The identity build writes an isolated bundle to `build/identity-theme/gitmora`, including content-hashed CSS/theme/language bootstrap filenames and a generated properties file. It never rewrites committed properties; successive builds reproduce the same fingerprints. `deploy/identity/Dockerfile` copies that bundle. Account templates fork only the pinned Keycloak 26.8.1 login/password/registration/profile forms to remove positive tabindex and forced mobile autofocus, while preserving native actions, scripts, passkeys slots and form fields. Upstream code remains Apache-2.0 with the supplied license; review these overrides with a provider upgrade.

`make smoke-design` runs the signed real API/PostgreSQL workspace matrix. `make verify-ui` adds source, settings, key/account, CLI and actual provider registration workflows with axe-core checks in both themes at desktop/mobile widths. Rich Tasktopia views use the existing source-backed fixture through `make smoke-work-tasktopia` with `GITMORA_UI_AUDIT=1`, `TASKTOPIA_SOURCE` and Node 24. Screenshots/structured accessibility results live in ignored `output/playwright/design/`.
