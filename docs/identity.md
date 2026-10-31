# OIDC and browser sessions

The authentication backend supports a self-hosted OIDC provider using Authorization Code, S256 PKCE and nonce. It accepts an external identity only after signature/issuer/audience/expiry/issued-at/nonce/authorized-party checks. An existing exact local binding is required while registration is closed. When explicitly enabled, [registration](accounts.md) may create a new account for that verified issuer/subject, with no project/operator rights; email/profile/groups never select an existing account. Disabled/deleted bindings cannot re-register. Existing API/MCP keys continue to work independently. The [human workspace](web.md) implements account/binding administration, personal key lifecycle, safe login recovery and context inspection. The [native CLI flow](cli-auth.md) explicitly approves independent OIDC-bound roots held in Darwin Keychain/Linux Secret Service. Provider back-channel logout and external permission synchronization are not implemented.

## Configure a local provider

The [operator-owned Keycloak profile](identity-hosting.md) prepares a separate optimized provider/database and explicitly retires bootstrap authority. The shared-origin profile serves it under `/auth`, with scoped provider cookies and private administration. Actual optimized-provider gates are separate from the signed protocol fixture below.

Register a confidential OIDC client with the exact RP callback, authorization-code grant, S256 PKCE and RS256 ID tokens. The managed same-origin profile uses `/auth` and the existing trusted TLS origin; a separately hosted provider remains supported. Use a dedicated client secret. Example:

```sh
WORKGRAPH_OIDC_ISSUER=https://identity.internal/realms/workgraph
WORKGRAPH_OIDC_CLIENT_ID=workgraph
WORKGRAPH_OIDC_CLIENT_SECRET=<dedicated server-side client secret>
WORKGRAPH_PUBLIC_URL=https://workgraph.internal
# Optional; basic is default, post supports explicit client_secret_post providers:
WORKGRAPH_OIDC_AUTH_STYLE=basic
# Optional; 900..28800 seconds, default 28800:
WORKGRAPH_OIDC_SESSION_SECONDS=28800
```

The issuer must match discovery and signed `iss` exactly. The public URL is a canonical origin, without path/query/userinfo. No request Host or forwarded-header value selects a callback or CSRF origin. Same-origin discovery/token/JWKS endpoints are mandatory. Server DNS results must all be private/loopback; no public destinations, link-local metadata services, ambient HTTP proxies or redirects are used. HTTPS is required outside loopback development. Normal platform TLS CA trust applies; no skip-verification option exists. Code/token/JWKS responses are bounded. The tested signing policy is RS256; other algorithms/providers need explicit compatibility work.

Discovery happens on login, not API startup, and caches metadata briefly. A provider outage returns a stable login error while bearer API/capture and current local sessions remain usable. Transient failures never enable another provider/fallback. No access, ID or refresh token from the provider is stored; no userinfo call or cloud fallback occurs.

API nodes sharing a database must use identical OIDC/public-origin/session policy settings. Policy changes/disable revoke existing browser grants, their delegated children and pending flows. Returning to an earlier configuration cannot revive them. Client-secret-only rotation preserves public identity policy. The ordinary synchronization/index worker does not activate or change browser policy.

## Bind identities explicitly

For hosted password accounts, use [the hidden-password operator command](accounts.md), which creates the provider account and exact local binding together. Assign memberships through [directory administration](directory.md). For an independently provisioned provider identity, an installation operator with `directory:read,platform:manage` can register its exact opaque subject through “Привязки входа” in the workspace or the API:

```http
POST /v1/admin/identities
Authorization: Bearer <operator credential>
Content-Type: application/json

{"request_id":"<new UUID>","id":"<new binding UUID>","user_id":"<existing enabled user UUID>","subject":"<exact provider sub>"}
```

The issuer is taken from server configuration, never the body. Subject is a case-sensitive bounded printable ASCII identifier. One `(issuer,subject)` can never be transferred to another local account, even after disable. Use a new provider subject/binding for an intentional identity change. No matching by email, display name or provider group.

`GET /v1/admin/identities?after_id=<UUID>&limit=30` returns operator-only binding metadata for the configured issuer. `PUT /v1/admin/identities/{id}` accepts `request_id,expected_version,enabled`. Every accepted binding edition closes previous session roots/delegated children; enabling does not restore old grants. The same actor/request/input replays its original content-free directory receipt. Local account disable/delete also blocks login and immediately invalidates its credentials.

Fresh setup can use its bootstrap user after explicit management-scope issuance; upgraded installs can recover operator status with `grant-operator`. Do not grant provider groups a local role implicitly. Current project membership/role and current installation-operator state remain mandatory even though the interactive credential contains the supported UI operation capabilities.

## HTTP browser contract

| Endpoint | Behavior |
| --- | --- |
| `GET /v1/auth/config` | Public enabled flag, configured issuer and login path; no client secret |
| `GET /v1/auth/login` | Set a short-lived HttpOnly flow cookie and redirect to the configured authorization endpoint; no arbitrary return query |
| `GET /v1/auth/oidc/callback` | Consume exact state/browser binding once, exchange code with PKCE, verify ID token/local binding, issue fresh cookies and redirect to fixed RP root |
| `GET /v1/auth/session` | Cookie-only session ID/user ID/expiry; no credential/hash/CSRF secret |
| `POST /v1/auth/logout` | Same-origin CSRF-protected local logout and descendant revocation; clear cookies |

HTTPS cookies are `__Host-workgraph_session`, `__Host-workgraph_csrf`, `__Host-workgraph_login`. They are host-only, Secure, Path=/, SameSite=Lax. Session/login cookies are HttpOnly. Only the CSRF cookie is readable by the same-origin UI. Loopback HTTP uses corresponding `workgraph_*_dev` names; these are development-only and not the production cookie contract. Cookies do not isolate services sharing the same hostname on different ports; deploy distinct hostnames.

For every unsafe API method authenticated through the session cookie, send:

```http
Origin: https://workgraph.internal
X-Gitmora-CSRF: <current readable CSRF cookie value>
```

The server verifies exact Origin, the header/cookie and stored session CSRF hash, and rejects cross-site fetch hints/duplicate cookies. This applies to POST search as well as mutations. GET/HEAD reads do not mutate sessions or provider state. All authentication responses use no-store and no-referrer. The callback does not expose provider codes/tokens or cookie values in a JSON body. Failed login/code exchange requires a new flow; old state cannot be retried. Pending state expires after ten minutes, with a global 1,000-row cap.

With a bundled UI, failed login-start/callback GETs explicitly preferring `text/html` over `application/json` return 303 to the configured public root and a fixed `#login=failed`, `#login=unavailable` or `#login=busy` notice. Disabled OIDC uses the relative local root. Request Host, provider text and proof parameters never select the destination or notice content. The UI removes recognized fragments and can show a failure while an earlier session remains valid. Wildcard-only/equal-or-higher JSON preferences, other routes and API-only servers retain the original JSON status/error contract. Capacity recovery includes `Retry-After: 60`.

Explicit Authorization headers use their own bearer authority and ignore cookie authentication. A session cookie cannot be used as a bearer token. The underlying internal browser-kind credential has a separate undisclosed random secret and is excluded from bearer lookup. `/mcp` stays bearer-only; agents/collectors use explicit scoped api-kind keys. Integrations/webhooks keep their own authentication contracts.

Sessions survive a normal API restart and expire at a fixed deadline, by default eight hours. At most ten live browser grants are kept per user; a new verified login evicts the oldest if necessary. A successful relogin replaces the old browser grant, while a failed relogin leaves it valid. Account/binding/configuration/token state is checked from PostgreSQL on each request. Expired metadata and all-platform retention policy remain future operations work.

Cookie-authenticated `POST /v1/me/tokens` uses the existing limited delegation policy: a child cannot exceed the current session's grant/expiry, and logout/disable revokes descendants. The workspace's personal key view exposes this policy, first-only secret delivery, metadata-only uncertain replay and confirmed subtree revocation. It clears private views at the declared local deadline; the server clock remains authoritative. Longer-lived collector credentials can use explicit native browser-assisted enrollment: the independent root survives ordinary browser logout but is revoked with local account/binding/configuration changes. Trusted offline issuance remains available. Logout does not delete immutable capture/task/code history.

Logout affects Gitmora only. The provider's own SSO session may remain, and clicking login again may authenticate through it. Provider-side disable/global logout is not instant local revocation without a back-channel integration; use Gitmora account/binding disable for immediate closure or wait for the configured deadline. Back-channel/provider-wide logout, external ACL sync and production provider coverage remain unfinished.

## Verification

`make smoke-identity` builds the RP and a dedicated synthetic local provider with real RSA signatures, then exercises a browser cookie jar against PostgreSQL. It covers explicit binding/no provisioning from admin email/groups, state/PKCE/nonce, foreign-browser/replayed callback denial, CSRF and bearer/MCP isolation, API restart, limited key delegation, binding/account/logout/config disable and no resurrection after rollback. Provider outage leaves bearer API available. This is protocol/process evidence, not rendered UI or live provider acceptance.

Unit/race contracts add invalid issuer/audience/expiry/iat/nbf/azp/signature, foreign discovery endpoints, redirects/duplicate/oversized metadata, cancellation, secure cookie attributes and secret serialization. PostgreSQL tests add one-use concurrent flow consumption and immutable mapping. `make verify` and Linux gates include identity.

`make smoke-credentials` adds actual rendered binding/key/recovery workflows with signed local OIDC and PostgreSQL, including no auto-provisioning, failed relogin preserving the old session, lost issuance response, scope/deadline bounds, account isolation and no resurrection after binding re-enable. This browser fixture is distinct from actual provider/production acceptance.


Registration and operator-created accounts are described in [accounts](accounts.md). A signed new subject can provision a new local identity only under the current durable signup policy. It never matches an existing user by email/name or grants project/operator authority.
