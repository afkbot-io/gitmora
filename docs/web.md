# Human web workspace

The React/TypeScript human workspace uses Tailwind CSS for all screen styling and implements unified hosted account sign-in and registration, project/repository/account/member administration, explicit OIDC bindings, personal credential lifecycle, GitLab/Tasktopia connection setup and health, task/session/MR navigation, lexical context search and exact source inspection. The current acceptance contract is C01–C18.

## Build and run

Each repository has a CLI/agent setup disclosure with the exact server/repository commands, Codex/OpenCode enablement, native stdio MCP and local queue/disable controls. Commands contain no bearer key. Downloads target the matching immutable core client release; publish its artifacts before the frontend. Accounts explain the registration policy and hidden-password operator command; a directory-only record is not presented as a newly provisioned login.

```sh
npm ci
make build
```

Start `bin/workgraphd serve` with the existing database and configured OIDC account settings, then open its root URL. The API binary contains the UI, Cyrillic/Latin fonts and runtime dependency licenses. Production runtime needs no Node, frontend cloud or CDN. Configure TLS and a dedicated hostname through the deployment boundary; public URL/cookie/CSRF rules remain in [identity](identity.md).

Build before compiling Go directly. An unbuilt checkout deliberately serves 503 at `/`, rather than a placeholder application. API/health routes still work independently. `GET /assets/third-party-licenses.txt` contains runtime MIT/OFL notices. Generated assets are ignored and rebuilt from the lockfile. Do not run an asset-generating build concurrently with Go compilation/verification that embeds those assets.

Asset response types use an explicit JS/CSS/WOFF/WOFF2/SVG/text allowlist, independent of system MIME files. `make verify-linux` also runs the static transport tests in a scratch container as an unprivileged user without network or writable root; fonts remain correctly typed even there.

For development, `npm run dev:web` starts a loopback Vite server proxying `/v1` to `127.0.0.1:8080`. Vite uses development scripts/styles rather than the production CSP. The preferred OIDC test/deployment is the bundled same-origin Go server; testing development OIDC requires configuring its exact public origin separately.

## Public entry, preferences and language

The public root is a compact presentation with a sign-in link, optional registration link and a small labelled example of task/session/commit/MR relationships. It is not an authentication form or a CLI installation page. Sign-in/registration use the shared centered hosted account shell.

Appearance controls exist only in personal “Настройки”, available to every signed-in user. Project settings remain separate. The public root and hosted account forms expose language selection, without theme controls; they inherit the saved appearance. Russian is the default and English is available throughout the authored interface. A selected language passes through a strict `locale=ru|en` presentation parameter into the fixed provider authorization URL; provider selection/callback/state/proofs cannot be chosen by the caller.

Only the validated `gitmora.theme` and `gitmora.locale` preferences enter local storage. Authored labels/errors use an explicit translation catalog; user/project names, source text, identifiers, commands and evidence are not machine-translated. The provider renders its own localized forms and saves that chosen language for the returning application. `npm run test:web` checks complete translation coverage, placeholders and stale copy in addition to behavior tests.

## User flow

“Войти” on the one-screen presentation uses the hosted same-origin account and signed code/PKCE/nonce flow. Signup appears only when WORKGRAPH_REGISTRATION_ENABLED is true; exact verified subjects are created without implicit membership or operator grants. With signup closed, use the hidden-password operator command in [accounts](accounts.md). Explicit binding remains available for external-provider recovery. Browser transport accepts account cookies and CSRF only; the personal-key entry and in-memory bearer mode have been removed. API keys remain CLI/MCP/API credentials and can be managed after signing in.

Catalog reads need `directory:read`, or the appropriate existing operator metadata authority. Context needs at least one source read capability and an actual project role. Original four-scope bootstrap keys do not silently acquire directory permissions. An operator can inspect project metadata without membership but cannot search its content. All requests are independently authorized by the server.

Select a project, optionally a repository/type/historical-versions filter, and enter lexical terms or a complete commit SHA. Full SHA uses the backend's confirmed-commit relation semantics. Search returns at most twenty fragments; when more match, narrow the query. The display distinguishes pending/failed indexing, no matches and denied/unavailable requests. Loaded project/repository lists expose their continuation instead of silently stopping at the first page.

Open a result to read its exact original version. The inspector includes task/session/revision/repository/file/commit identity when present, observation time, exact rune or code-line range, digest and evidence ID. Long chunks expose continuation and a return to the start. “Последняя запись” means the latest observed source record, not verified semantic truth or current branch applicability. Confirmed GitLab commits are explicitly separate from unconfirmed agent records. Source HTML/instructions remain quoted text.

Changing project/query/filter clears earlier evidence and cancels its requests. Late responses cannot fill the new scope. A 401 or the credential's declared deadline returns to login and removes visible workspace data. The browser timer does not extend server authority. Logout hides evidence immediately, fences pending requests and closes the cookie session with CSRF. A failed server logout offers a visible retry; do not mistake a local screen change for successful server revocation. Organization login stays disabled while that logout/authentication check is pending.

Browser login failures return to the fixed workspace root with one safe local notice. The UI removes the recognized fragment and displays the notice even if a previous session remains valid. Provider messages, codes and state are not reflected. Explicit JSON/API clients retain JSON errors; [identity](identity.md) describes content negotiation and provider limitations.

## Verification

```sh
npm run check:web
npm run test:web
npm run check:format:web
npx playwright install chromium
make smoke-web
```

Browser installation is a development/test step; the application does not download a browser at runtime. `make verify` includes the browser smoke and expects its pinned Chromium already installed. `make smoke-web` builds assets/API/IdP fixture, uses synthetic isolated PostgreSQL and runs actual Chromium. A missing browser produces an actionable Playwright installation error; there is no fixture-only fallback. `python3 scripts/smoke-web.py --serve` keeps the same synthetic processes alive for CLI visual inspection and prints only their URL. Stop it with SIGTERM/Ctrl+C for cleanup.

Screenshots are generated under ignored `output/playwright/`. The fixture copies no Tasktopia database/configuration, uses no external AI endpoint and never touches production GitLab. Source/scoping/CSRF, signing, indexing and revocation are real local process paths. Error/delay injections exercise failure handling, rather than substitute successful source data.

The standalone Tailwind CLI consent screen shows the exact account/code/device/capabilities/deadline and uses fresh OIDC authentication, cancel-first approval/rejection and terminal cancellation. Its key/proofs remain outside the browser; see [native authentication](cli-auth.md). Remaining acceptance follows [C01–C18](implementation-status.md), including current source coverage, usability and deployment checks.

## Catalog, accounts and connections

“Проекты” exposes authorized catalog entries and lets an installation operator with `directory:read,platform:manage` create a project. The creator becomes its administrator. Project/repository names can change; provider base/numeric identity cannot. Repository registration uses a server URL and positive exact numeric GitLab project ID. Browser creation bounds IDs to safe integers; oversized legacy int64 IDs are labelled rather than rounded. Mutations address Gitmora UUIDs.

“Настройки проекта” includes renaming, members, Tasktopia and per-repository GitLab. Catalog edits require the existing directory policy; integration edits require actual project-admin membership and `integration:manage`. An operator without membership receives no integration authority. Independent integration-read capability controls status reads. Member add/change/remove uses the current membership version and existing user UUID. Lists expose continuation. “Учётные записи” shows registration policy and the operator creation command, and lets scoped installation operators rename/enable/disable/delete users and manage operator grants. Signup or operator creation configures login, while project access is assigned separately. The separate binding and personal-key views are described below.

Metadata writes carry current expected versions and a request UUID retained in memory for an exact retry. If an ACK is lost, retry unchanged fields to recover the original receipt; editing scope/version/content starts another command identity. The server still detects stale versions and protects the last enabled administrator/operator. Refresh project/account/member data before making an intentional new edit after a conflict. Browser pending fieldsets stop edits while a command or metadata refresh is underway. Parent project versions refresh after child catalog changes without changing current navigation.

GitLab setup verifies its immutable catalog scope before saving a replacement API key plus either signing token or legacy webhook secret. Configure the returned webhook URL in GitLab yourself; the UI does not silently create remote hooks. Status distinguishes enabled/blocked/disconnected from observed last sync, next attempt and individual collection streams. “Запросить сверку GitLab” queues work; the worker must be running. “Обновить состояние” fetches current stored health rather than performing a synchronous provider scan.

Tasktopia setup verifies the explicit country through MCP. Saved endpoint/country fields remain read-only across replacement/disconnect. The automatic-MR checkbox is an explicit persistent policy and needs a dedicated token with the appropriate write scope. It does not alter task stage/acceptance. Manual UUID task refresh queues an authorized read; ordinary bound capture discovery remains automatic. Re-enter a valid token when changing policy or reconnecting. All provider password fields clear on submission/cancel or project/role loss, never prefill saved secrets and never enter the metadata replay helper/browser storage. A manage-only CLI/API key can save/disconnect without permission to read source status; disconnect clears its previous ACK status.

Native confirmations describe local consequences and focus cancel first. Escape/close does not execute deletion. Local project/repository deletion does not remove GitLab repositories or Tasktopia tasks. User disable/delete revokes credentials/sessions under server policy; re-enable restores neither old keys nor operator grants. An external request already dispatched may finish after local cancellation/disconnect.

```sh
make smoke-settings
TASKTOPIA_SOURCE='/absolute/path/to/Tasktopia' \
TASKTOPIA_NODE='/absolute/path/to/node24/bin/node' \
make smoke-settings-tasktopia
```

The first command uses actual browser/API/PostgreSQL/signed local OIDC and a GitLab HTTP fixture; Tasktopia's server-unconfigured state is checked. The second reuses the existing isolated actual-source Tasktopia harness and drives real MCP connection/policy/task-refresh through browser forms. The original `make smoke-tasktopia` API workflow remains a separate regression gate. No production source credentials/data are used.

## Bindings and personal credentials

“Привязки входа” is available to installation operators with `directory:read,platform:manage`. Create the local account first, then enter its exact user UUID and case-sensitive provider subject. The issuer comes from server configuration. Existing user/issuer/subject values are immutable; enable/disable uses the observed version and confirmation. Disabling a binding revokes its browser sessions and descendant keys. Enabling requires a new login and restores no old key. Profile names/groups never select an existing binding or grant project authority; enabled signup creates a new exact verified subject only.

“Личные ключи” requires `credentials:manage` independently of catalog access. It shows owned API/browser records, their parent, permissions, deadline and revocation state. A browser record is a session, not a reusable API secret. Issue a key with an explicit label, selected available permissions and lifetime in minutes. No permissions are selected automatically. The maximum is the lesser of the parent's remaining lifetime and 90 days; a browser parent is normally limited to eight hours. Longer-lived collector roots use the native browser-confirmed CLI login. Project roles are still checked separately.

Save the first displayed secret yourself, then close its disclosure before issuing another key. Gitmora does not persist it in browser storage or copy it automatically. Closing/navigating away/authentication loss discards its view state; observed expiry/revocation hides it. An unchanged retry after an uncertain response retains the same command ID/payload. If the server already accepted issuance, it returns metadata without the secret: revoke that inaccessible key and issue a new one. Revocation confirms the whole descendant tree, including the current session when applicable. Logout revokes children issued from that browser session after the server acknowledges it; independent root keys remain valid.

`make smoke-credentials` runs actual Chromium against built API/PostgreSQL and a signed local provider, including lost issuance response, independent restricted delegation, exact binding/version changes, account isolation, subtree/logout/binding revocation, failure recovery and mobile layout. It is included in `make verify`. `python3 scripts/smoke-credentials.py --serve` offers the isolated fixture for headed CLI inspection.

## Tasks, session windows and merge requests

“Работа” provides paginated source lists and navigable task/session/MR context. Session transcripts expose exact revisions and message continuation. Task children retain source counts and truncation. MR refresh preserves the expected head and displays only SHA-scoped CI; adopting a changed observed head is an explicit action. [Work navigation](work-navigation.md) documents source/capability/availability rules, UI behavior, API/MCP discovery and numeric compatibility limits.

`make smoke-work` is included in `make verify`. `make smoke-work-tasktopia` uses the same actual-source/Node 24+ configuration as the Tasktopia settings gate. The independent original API-only regression still runs through `make smoke-tasktopia`.

## Tailwind styling

Tailwind CSS 4.3.3 is compiled through the matching official Vite plugin. Theme colors/fonts, inclusive `mobile` (≤700px), `compact` (≤1100px) and `wide` (≥1600px) variants and shared element defaults live in `web/src/styles.css`. Screen/layout/state styling lives in complete utility strings in the views. Repeated button variants are typed complete strings in `web/src/views/ui.ts`; do not construct partial utility names dynamically. Semantic class markers support scoped descendants/states and browser inspection, not an independent component stylesheet.

Theme/utilities are imported without Preflight to preserve existing base heading/list/form behavior. Shared base rules use Tailwind `@apply`. Fonts, generated CSS and the Tailwind license are bundled locally; production needs no Tailwind/Node runtime or CDN. Use the existing `npm run build:web`/`make build`, formatting and browser gates.
