# Host the account provider

The managed profile uses optimized Keycloak 26.8.1, its own PostgreSQL and private administration. The app and provider can share one trusted HTTPS origin with the provider under `/auth/`. [Identity](identity.md) describes the OIDC and cookie contract.

## Prepare and finalize

Build the matching images with `make build-image` and `make build-identity-image`. `/auth` and private management paths are baked into the optimized image; runtime settings alone cannot change them. For a separate-host root profile, use `make build-identity-root-image` instead.

Choose private existing parent directories and a new identity directory. Replace the public origins with your installation:

```sh
bin/gitmorad identity-server init \
  --directory /absolute/private/identity-state \
  --compose /absolute/checkout/deploy/identity/compose.yaml \
  --public-url https://gitmora.example.com/auth \
  --application-url https://gitmora.example.com \
  --identity-image gitmora-identity:26.8.1-auth \
  --gateway-image gitmora:local \
  --port 18081
bin/gitmorad identity-server up --directory /absolute/private/identity-state
bin/gitmorad identity-server finalize --directory /absolute/private/identity-state
bin/gitmorad identity-server status --directory /absolute/private/identity-state
```

Initialization refuses existing state and creates a `0700` directory. Its `operator.json` contains private administrative, initial-user, client and database credentials. Never paste it into logs/chat/source control. Container-readable secrets stay behind the private directory boundary. Back up the separate identity database as well as application state.

`finalize` verifies the exact selected loopback gateway/project/service/image before sending credentials. It creates and proves permanent private administration, verifies realm/subject/client, callback/origin, code flow/S256/RS256 and secret, then retires bootstrap authority. It moves the initial import out of the mounted bootstrap directory and recreates the provider without initial account imports. Repeating finalization verifies existing authority rather than resetting passwords. Interrupted recreation can resume. Never remove named volumes during an ordinary stop/update.

The provider has a 2 GiB memory ceiling and 128–512 MiB JVM heap bounds; its PostgreSQL has a separate 512 MiB ceiling. These are resource policies, not capacity benchmarks. Only the credential-free gateway has ingress and numeric-loopback publication. PostgreSQL and management port 9000 have no host ports.

## Activate the application

1. Adapt [the shared-origin nginx example](../deploy/nginx/gitmora.https.conf) to the real domain/certificate. Expose only the selected realm and resources; keep master/admin/management private.
2. From the application's private network, verify issuer discovery over normal trusted TLS. Adapt [the API-only host mapping](../deploy/compose.identity.yaml); public DNS alone does not satisfy the private-address policy.
3. Copy only the provider's `secrets/oidc_client_secret` to the application's existing `secrets/oidc_client_secret` slot, preserving the private file boundary. Do not put its value in argv or shared runtime output.
4. Adapt/select [the API-only activation overlay](../deploy/compose.identity-auth.yaml). Set exact issuer, client and `WORKGRAPH_OIDC_CLIENT_SECRET_FILE=/run/secrets/oidc_client_secret`. Recreate the selected API so the changed policy/mount is active; ordinary workers need no identity policy.
5. Through the privileged application API, bind the finalized provider's exact `user_id` subject to the initial local operator. [The binding contract](identity.md#bind-identities-explicitly) gives the request shape. A matching username/email/group is never sufficient.

The generated first realm account is `gitmora.admin`. Only its owner retrieves the initial `user_password` from private operator state, chooses a replacement password and completes the profile. The distinct `gitmora.operator` belongs to private master administration and is not the application login.

The IdP gateway strips application cookies/bearer keys and confines provider cookies to host-only `/auth` paths. Same-origin apps still share a browser origin; path/cookie controls are not origin isolation.

## Users and CLI

[Registration policy](accounts.md) defaults to closed. To enable it, apply the same literal env value to the API and provider. Signup creates a new ordinary exact-subject account without project/operator authority. With signup closed, `gitmorad users create` prompts for a hidden password and creates the provider/local binding together. Assign project membership separately.

```sh
gitmora auth login --server https://gitmora.example.com
```

Developers compare their terminal/browser code and approve requested scopes/deadline after fresh OIDC authorization. Existing provider SSO may complete that flow without a second password prompt; explicit Gitmora consent is still required. Native roots are stored in the OS secret store. [CLI authentication](cli-auth.md) explains independent browser/terminal logout.

SMTP recovery, public master console, provider back-channel logout and automatic external permission synchronization are not enabled by this profile. Immediate local revocation uses account/binding disable.

## Verification

`make smoke-keycloak` runs the optimized provider, distinct PostgreSQL, durable finalization/recreation, real RP and Chromium under a loopback `/auth` origin. `make smoke-keycloak-native` adds real CLI/system-secret-store/push/revocation checks. The harness owns and removes only its generated resources. Native nginx/OpenSSL are verification dependencies; the host trust store is not modified. `make smoke-identity-proxy` separately verifies trusted fixture TLS, routing, wrong CA/hostname denial and private administration. [Development](development.md) lists prerequisites and other gates.
