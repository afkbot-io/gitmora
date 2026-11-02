# Development and verification

Use the Go toolchain selected by `go.mod`, Node.js 22.22+, npm, Python 3.12+ and Docker Compose. Use GNU make; Go race tests require a C compiler (GCC/Clang and CGO enabled). Native/provider proxy gates also use nginx/OpenSSL. Confirm `python3 --version`: some macOS installations select an obsolete framework interpreter. Put the supported interpreter on PATH before running the make targets. Install dependencies and the pinned browser engines:

```sh
npm ci
npx playwright install --with-deps chromium webkit firefox
make build
```

On Linux, Playwright may additionally need its documented system libraries. The Go toolchain can be downloaded automatically when required. Never supply production credentials to automated tests.

## GitHub Actions

The [CI workflow](../.github/workflows/ci.yml) runs on pull requests, pushes to `main` and manual dispatch. Linux/macOS source jobs build the application, check format, run Go vet/unit and TypeScript/agent tests, and validate configuration/docs. The Linux source job scans Git history with pinned Gitleaks. `.gitleaksignore` contains only two exact historical redaction-test fixture fingerprints; new findings still fail.

After source checks pass, the Linux release job runs `make verify-release`, then the separate `make smoke-load`. It provides nginx/OpenSSL, the pinned Playwright engines, Docker Compose and an unlocked isolated Secret Service for native CLI account tests. Its Codex fixture uses the published `@openai/codex@0.164.0-alpha.11.1`; OpenCode comes from `package-lock.json`. Agent prompts and provider responses are deterministic loopback fixtures; no model key or production credential is supplied. CI uses read-only repository permissions and commit-pinned official actions; it never deploys or publishes a release. Only the bounded load report is uploaded.

For the same complete local gate, install the prerequisites above and make the tested Codex executable available as `codex` or set `GITMORA_TEST_CODEX` to its absolute path. On macOS the installed app bundle is also detected. On Linux, `bash .github/scripts/verify-linux.sh` sets up an ephemeral D-Bus/keyring session after nginx/OpenSSL, `libsecret-tools`, `gnome-keyring` and `dbus-x11` have been installed. It does not use a personal keyring. On macOS run the following in the normal unlocked user session:

```sh
make verify-release
make smoke-load
```

A failed or skipped CI job is not a successful release gate. Inspect the actual workflow run for the source revision being reviewed. Public cloud-provider integration and physical-device acceptance remain separate from protocol/browser fixtures.

## Fast checks

`make check-config` validates complete production env documentation, supported example keys and local instruction links. `make smoke-self-host-config` uses the actual operator CLI/Compose for two independent custom-domain profiles. `make smoke-custom-server` executes Linux CLI/API/stdio MCP through real nginx HTTPS at a fixture-only custom DNS hostname, including certificate-name rejection. It changes no host DNS or trust store.

```sh
npm run check:web
npm run check:format:web
npm run test:web
npm run test:agents
make vet
make integration
python3 -m unittest discover -s scripts/tests -p test_release_cli.py
```

`make integration` starts isolated PostgreSQL at `127.0.0.1:55439` and runs the Go suite with the race detector. Tests create/drop their own schemas. `npm run test:web` also rejects missing/stale authored translations and mismatched interpolation placeholders. Do not regenerate embedded web assets while Go builds/tests are running.

## Process and browser gates

`make verify` runs the host vet/race, API/collector/agent, GitLab, context, directory, browser, deployment, restore, egress, provider and distribution gates. It takes longer and requires the binaries/browsers above. A full run is different from running only selected targets.

| Target | Scope |
| --- | --- |
| `make smoke-codex`, `make smoke-opencode` | Real local agent protocols/hooks, source capture, queue and ordinary Git delivery. OpenCode uses a labelled deterministic loopback model. |
| `make smoke-native-history`, `make smoke-compatibility` | Complete source windows, replay/restart, immutable Git import and provenance. |
| `make smoke-github`, `make smoke-github-ui` | Real Git/standalone CLI/offline push/API/worker/DB/search/MCP with a Git-backed GitHub protocol fixture; signed browser URL registration, public/token modes, RU/EN and desktop/iPhone layouts. |
| `make smoke-load` | Isolated 1,000-session dataset, 1/10/25/50 concurrent authenticated clients, bounded search/browse/expand/pack/MCP mix, durable restart, replay and membership revocation. |
| `make smoke-forge-subpath`, `make smoke-gitlab-profile` | GitLab fixture reconciliation under `/gitlab`, custom port/CA/CONNECT and explicit Compose policy. |
| `make smoke-context`, `make smoke-agent-context` | Real index worker, search, exact source/context and external-agent tool workflow. |
| `make smoke-directory`, `make smoke-identity` | Catalog/account roles, signed OIDC, cookies/CSRF, revocation and recovery. |
| `make smoke-web`, `make smoke-settings`, `make smoke-credentials`, `make smoke-history` | Signed browser/API/PostgreSQL workflows, races, denied access, uncertainty and mobile layout. |
| `make smoke-browser-compat` | Chromium/Android, WebKit/iPhone and Firefox layouts, both themes, RU/EN, keyboard, history and accessibility. |
| `make smoke-keycloak`, `make smoke-keycloak-native`, `make smoke-registration` | Real optimized provider, separate database, bootstrap retirement, signup policy and native account consent. |
| `make smoke-identity-proxy` | Actual nginx/TLS routes, wrong CA/hostname and private admin denial. |
| `make smoke-identity-permissions` | An image built from a committed private archive extracted with umask 077; actual UID-1000 template reads and registration. |
| `make smoke-deployment`, `make smoke-recovery`, `make smoke-egress` | Isolated persistent installations, file secrets, outage/reconnect, actual dump/restore and route isolation. |
| `make smoke-onboarding`, `make smoke-cli-release`, `make smoke-cli-downloads` | Executed checksum-checked installer, exact-byte update, queue/push/restart and public download routes. |
| `make verify-linux` | Linux runtime/process checks and unprivileged collector/Git/SQLite behavior. |
| `make verify-release` | Extended release gates, including native OS-store account and cross-browser workflows. |

Test fixtures remain synthetic even when real HTTP/Git/PostgreSQL/browser processes are used. A provider fixture does not establish compatibility with every upstream release. Phone tests are device emulation, not physical-device acceptance. Isolated smoke scripts clean only resources they create; do not use broad shared-host volume/container cleanup.


## Review and style

Domain packages own invariants and do not import SQL/HTTP/OS adapters. Application services use narrow consumer-owned ports; adapters handle Git, SQLite, PostgreSQL, provider HTTP and the OS secret store. Exported Go declarations need useful GoDoc. Keep stable typed errors and bound inputs; do not expose SQL/provider credentials through transport errors.

For behavior changes, reproduce the issue with a failing observable test, implement the smallest coherent fix, then run focused and affected integration checks. Review access control, races, transaction/replay semantics, cancellation, source fidelity and compatibility. Schema migrations are immutable after release; retire data with a new migration.

Tailwind styling uses complete static utility strings, shared semantic tokens and typed button variants. Theme controls belong only in personal settings. Keep source/user text inert and untranslated; authored UI copy uses the RU/EN catalog. [Web design](web-design.md) and [web behavior](web.md) describe the interface contract.

WebKit's native select and Playwright's screenshot synchronizer have two precisely reproduced style/CSP diagnostics recognized by the test helper. All other violations fail. Do not add broad suppression or weaken product CSP to make a browser gate green. Keep raw axe `incomplete` results and separately assess them rather than treating them as automatic passes.

## Bounded load checks

Run `make smoke-load` separately from other CPU/database-heavy tests. It never accepts a production URL, credential or database: the real API and worker use an owned schema in the dedicated Compose test database. The default dataset contains 1,000 synthetic sessions and distinct identities for up to 50 simultaneous clients. Each stage lasts 20 seconds. The test API uses an explicit PostgreSQL pool of 8 connections (`--database-pool-max` changes it); it does not infer database capacity from the host CPU count. Size the production pool against its database resources using the pgx DSN `pool_max_conns` parameter. The report records per-operation p50/p95/p99, throughput, errors and dataset bytes under ignored `build/load/`.

For a short diagnostic run, use `python3 scripts/smoke-load.py --sessions 100 --seconds 2 --clients 1 5`. Bounds are 50–5,000 sessions, 2–60 seconds and 1–50 clients. This loopback Python client does not measure public TLS, network delay, browser rendering or the capacity of a different production machine. Report the host and resource limits alongside results; concurrency is not a count of supported employees.
