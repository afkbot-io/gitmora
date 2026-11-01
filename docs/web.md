# Human web workspace

Gitmora uses React, TypeScript and Tailwind CSS. The authenticated application fills the viewport with a project sidebar and a scrollable main area. The sidebar contains Work, Search, Repositories, Members, Project settings and CLI/MCP. Project creation lives in the project selector; profile, language, appearance and optional automation credentials live in personal settings.

## First use and authorization

The public root presents the product and links to sign-in and, when enabled, registration. Hosted account forms use the same origin and visual system. OIDC is the internal authentication protocol; users do not configure provider bindings or paste personal keys to sign in. `WORKGRAPH_REGISTRATION_ENABLED` controls signup. With signup closed, an operator [creates accounts with the CLI](accounts.md).

A registered user can create a project and becomes its administrator. Registration grants no existing project membership or installation-operator authority. Project administrators add an already registered colleague by exact local nickname or UUID and assign reader, writer or administrator. Nicknames are edited in personal settings; UUIDs remain stable. There is no global user browser or partial-name directory search. The server verifies current roles, enabled accounts and credential scopes on every operation and protects the last enabled administrator.

Global user and identity-binding management remains available through the authorized operator API/CLI for installation maintenance, outside the normal workspace. An operator's metadata access does not grant content or integration access without project membership.

## Repository onboarding

An empty project offers a GitHub/GitLab connection form. Supply the full repository URL and a read token; the backend resolves and verifies the provider ID before registering the catalog entry and sealing credentials. GitHub public access without a token is an explicit opt-in and subject to the shared API rate limit. A rejected lookup does not create a catalog entry. If a later connection stage fails, retry uses the existing entry rather than making a duplicate. Token fields clear after submission and are never stored in browser storage.

The same upstream repository can be connected independently by different Gitmora projects. Its captures, permissions, connections and synchronization state remain scoped to each local repository UUID. Provider-instance identity also scopes associations between commits and PRs.


## Search and history

Search accepts lexical terms, a complete commit SHA or a synced MR/PR URL and offers repository, source and historical-version filters. Open a result to inspect the exact source. Recent sessions and MR/PR appear on the search home screen, with transcripts and SHA-scoped CI. Source instructions are rendered as quoted data. Changing the project or request cancels pending reads and removes earlier content; late responses cannot populate another scope.

Missing permissions, empty history, pending indexing, provider unavailability and failed requests are distinct states. Lists expose continuation. Source freshness and confirmation rules are enforced by the backend; the interface does not imply that an agent-reported SHA is already provider-confirmed. See [context](context.md) and [work navigation](work-navigation.md).

## CLI and MCP

The project setup page selects the target repository and supplies `install.sh`, browser-confirmed CLI login, initialization and supported-agent commands. The installer detects macOS/Linux architecture and verifies the binary and release manifest checksums before execution. Advanced details include platform downloads and MCP configuration. Commands contain no credentials. Publish immutable client artifacts before activating the corresponding frontend.

Native stdio MCP reads authorized project context using the local saved CLI login. Ordinary API keys remain available under personal settings for explicit automation. Their first secret is displayed once, uncertain issuance retries preserve command identity, and revocation/expiry/logout remove the appropriate descendant credentials. No browser storage contains authentication material. See [CLI authentication](cli-auth.md).

## Language, appearance and accessibility

Russian and English cover all authored UI strings, including errors and empty states. User content, repository names, code and commands retain their original values. The provider receives only a validated presentation locale. Theme controls exist only in personal settings; public and account pages inherit the preference. Both themes use shared color, spacing, typography, focus and control styles. Mobile navigation collapses into the project menu.

Local storage contains validated theme/language preferences and `gitmora.project.<user UUID>` for the selected project. The project preference conveys no authority: the server still checks membership. Logout clears visible data immediately and revokes the cookie session with CSRF protection. Failure offers a retry; a locally hidden screen is not treated as server revocation.

## Build and verification

```sh
npm ci
make build
npm run test:web
npm run check:format:web
make smoke-settings smoke-github-ui smoke-web smoke-history smoke-credentials
make smoke-browser-compat
```

The Go API embeds compiled assets, local fonts and runtime license notices; production needs no Node or CDN. Build assets before compiling Go. Do not regenerate embedded assets concurrently with Go compilation. An unbuilt checkout serves 503 at the public root while API/health routes remain independent. For local development, `npm run dev:web` proxies `/v1` to loopback; signed authentication tests use the bundled Go origin.
