# Native CLI authentication

Native enrollment uses the shared [account provider](identity.md), explicit browser consent and an independent credential in the OS secret store.

Developers can use the [standalone native installation procedure](native-installation.md); it needs no Go/npm/source checkout. The installation needs a configured provider and exact subject/account bindings. Users can sign up when enabled or be created by the operator; project membership is assigned separately.

## Explicit login

The API needs a configured local OIDC provider and an immutable subject/account binding. Project reads/writes additionally require current membership. The browser authenticates through that provider and explicitly confirms the device label, requested capabilities and lifetime. Enabled signup creates a new account only for the verified exact subject; profile claims never select an existing account or grant administration.

```sh
gitmora auth login --server https://workgraph.internal
gitmora auth status --server https://workgraph.internal
gitmora auth logout --server https://workgraph.internal
```

These commands work outside a Git repository. Login prints a public human code and fixed verification URL to stderr; safe final metadata goes to stdout. `--no-browser` leaves opening the displayed link to the user. Neither stream contains the candidate API key or recovery proofs. There is no token CLI argument, token file or plaintext fallback.

The default new key lasts 720 hours. `--ttl 48h`, `--label "Engineering laptop"` and `--scopes capture:read,capture:write,directory:read,credentials:manage` can restrict a new enrollment. Capabilities are explicit engineering operations; `credentials:manage` is required for authenticated logout. Lifetime is 60 seconds through 90 days. Platform/provider/catalog administration  are excluded. All API content still requires current project authority; a capability is not project membership.

The native client saves independent candidate/device/poll proofs in the OS store **before** registering their hashes. The public human code does not authenticate polling or API use. A confirmation window lasts ten minutes. Approval needs a fresh locally verified OIDC session and explicit current-browser consent. The dedicated fresh login preserves an existing same-account browser session and its fixed expiry; ordinary login semantics stay unchanged. This avoids invalidating that session's jobs/delegated keys merely to connect a terminal.

A new OIDC authorization flow can complete through an existing provider SSO session; a second password prompt is not required by this contract. Explicit Gitmora device/code/scope consent remains mandatory. The [self-hosted provider](identity-hosting.md) exercises this behavior with real Keycloak rather than assuming the fixture's automatic navigation represents a password challenge.

## Storage and noninteractive delivery

macOS uses the current user's Darwin Keychain through the system `security` utility. Linux requires `secret-tool`, an unlocked Secret Service collection and the current user's D-Bus session. Headless Linux without a secret service fails explicitly; existing environment injection remains available. `WORKGRAPH_KEYCHAIN_PATH`, when supplied on Darwin, must identify an existing private regular native keychain file; no alternate file format is supported. It is rejected on Linux.

The exact canonical server URL, including any application path, selects a distinct OS-store slot. Current OIDC `PublicURL` is a root origin; a prefixed reverse-proxy login is not implied by the namespace codec. Host case/default ports/trailing slash normalize to the same identity; credentials never follow redirects to another destination. HTTP is accepted only for explicit loopback development. Private TLS relies on the system trust store. Authentication HTTP has no cookie jar or ambient proxy.

Darwin interactive commands and Linux store operations receive the opaque private envelope through stdin. No key/proof is put into process arguments, a shell, the collector's SQLite database, repository config or log output. Short subprocess deadlines and bounded private output apply. Per-server mutation locks are empty private files; they contain no authentication state.

After `gitmora init --server ... --repository-id ...`, ordinary installed Git hooks and `gitmora sync` read the active OS slot. They never initiate login or open a browser. The existing two-second hook budget still applies; inability to read/send retains queued evidence. One native credential is read per drain batch. A configured token environment variable has explicit precedence: an empty/invalid present value fails instead of silently using another credential. Unset that variable to use native storage. API scope/membership/revocation is rechecked on every upload.

## Interrupted login, replacement and logout

Rerun the same `auth login --server ...` to resume a pending intent. Omitted options preserve its label/scopes/lifetime and independent candidate. Changed options conflict. A lost preparation/approval response or Ctrl-C retains OS-owned recovery state. Before polling or enrolling again, the client tests its saved candidate through ordinary `/v1/me`; an already approved key can recover even after its ephemeral request expires. Successful activation saves safe account/token metadata before reporting completion.

An expired/unavailable unapproved window requires `auth logout` before a new explicit login. `auth login --replace` first cancels/revokes the saved state, verifies closure and then starts a new request; ambiguous remote failure preserves recovery instead of overwriting it. Approval is terminal and concurrent retries cannot create duplicate roots.

Default logout authenticates and revokes its exact independent root, then verifies the candidate is no longer accepted before deleting the OS entry. Pending logout proves device ownership and atomically cancels the request. If browser approval wins that race, logout authenticates/revokes the newly created root. A missing ephemeral flow is followed by another candidate check before local deletion. Cancellation may close expired pending rows as well, fencing approvals that were waiting for the same transaction lock.

`auth logout --local-only` explicitly forgets only this device's recovery state. Its output states that remote revocation was not verified; use the browser personal-key view to revoke an outstanding root when needed. Browser logout alone keeps an independently approved CLI key usable. Account/binding disable, binding edition changes, deployment OIDC policy/configuration change/disable/rollback and explicit root revocation invalidate it and its descendants.

## Verification

`WORKGRAPH_TEST_NATIVE_KEYCHAIN=1 go test ./internal/identity/keychain -run TestActualNativeSecretStoreRoundTrip -v` performs opt-in native save/read/update/delete in one random synthetic Gitmora server slot and cleans only that slot. Protocol fakes separately check secret-free process arguments, byte budgets and safe error boundaries; fake protocols are not evidence of a real OS backend.

Real PostgreSQL race tests cover immutable intent, fresh browser consent, approval/cancellation races, current root/binding/configuration authority and revocation without resurrection. Native application tests cover protected persistence before registration, exact uncertain recovery, candidate verification, no network-failure replacement and pending logout.

`make smoke-cli-consent` runs actual signed browser/API/PostgreSQL enrollment without requiring an OS store. `make smoke-cli-auth` additionally uses the built CLI and the actual current user's native store for process/API restart, lost approval acknowledgement, ordinary no-env Git push, authenticated logout and pending cancellation; it uses one random synthetic server slot and cleans that slot. `make test-native-secret-store-linux` verifies real Linux Secret Service in an ephemeral unprivileged D-Bus/GNOME collection inside the dedicated test container. These synthetic gates do not prove your production provider/network configuration; verify that installation separately.
