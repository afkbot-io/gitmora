# GitLab connection and commit verification

The implementation includes encrypted connection administration, authenticated durable webhook receipt, independent commit verification, paginated MR/CI/deployment projections and guarded source-to-squash/merge lineage. Collection state and incomplete evidence remain explicit. [Self-managed setup](gitlab-self-managed.md) covers custom HTTPS hosts, ports, installation prefixes and corporate CAs. Local project membership and the configured service account define access to the replica; automatic per-user provider ACL synchronization is outside the current core contract.

Configure the exact GitLab API origin and numeric project ID for each repository. A namespace/project page URL is not the server origin. Gitmora verifies access to that exact project before storing its dedicated credential. Use [the setup guide](gitlab-self-managed.md) for custom HTTPS ports, installation prefixes and corporate CA. Version-specific compatibility must be checked against your own instance.

## Runtime configuration

API and worker processes need the same database and encryption keyring:

```sh
export WORKGRAPH_DATABASE_URL='<PostgreSQL connection string>'
export WORKGRAPH_ENCRYPTION_KEYS='{"primary":"<base64 encoding of 32 random bytes>"}'
export WORKGRAPH_ENCRYPTION_ACTIVE_KEY='primary'
export WORKGRAPH_GITLAB_ALLOWLIST='https://gitlab.internal'
gitmorad migrate
gitmorad serve --listen 127.0.0.1:8080
# Separate process with the same configuration:
gitmorad worker
```

Keep encryption keys outside PostgreSQL, preserve them with an independently protected backup and supply them through your deployment secret mechanism. There is no built-in default key. The keyring accepts up to 32 named 256-bit keys. The active key encrypts new connections; retained historical keys decrypt existing envelopes. For rotation, first add the new key to every API/worker keyring while keeping the old active key, and restart those instances. Then make the new key active and restart them. Update connection credentials to re-encrypt envelopes; retain old keys until no stored envelope or restorable backup requires them. Removing a required key blocks reconciliation with `credential_unavailable`.

The allowlist contains comma-separated exact GitLab base URLs, including an installation subpath when needed. HTTPS is required except for loopback development fixtures. Requests never follow redirects or ambient HTTP proxy settings. URLs from webhook payloads are not used as request destinations. System trust roots apply to TLS; install the corporate CA in the runtime trust store when necessary.

The deployed build supports explicit `WORKGRAPH_GITLAB_PROXY_URL=http://gitlab-egress:8080` for the [selected GitLab.com profile](../deploy/compose.gitlab-com.yaml). The proxy URL must be an exact private HTTP origin/port without credentials/path/query. Its DNS/dial is private-only; GitLab HTTPS still uses the original hostname and normal certificate verification inside CONNECT. The separate relay accepts only `gitlab.com:443`, pins checked public IPv4 answers and receives no provider/database/keyring mounts. API/worker remain internal-only. The unauthenticated route probe checks transport, not credentialed project access; [the egress tests](../scripts/smoke-egress-compose.py) exercise the profile.

If all provider environment variables are absent, capture/retrieval remain available and provider integration routes return 503. Partial or malformed provider configuration prevents startup. `worker --once` attempts one due GitLab metadata batch, one independent code unit and one source-index job; normal worker mode polls for durable work and shuts down on SIGTERM. Confirmed commits also feed [verified code evidence](code.md), with separate discovery/file leases, safe health and bounded read-only agent quotes.

## Gitmora administration credential

Connection writes require both an `integration:manage` bearer capability and the current project's `admin` membership role. Reading health requires `integration:read` and a project read role. Capture capabilities alone cannot administer integrations. New bootstrap credentials include these capabilities; previously issued credentials are not silently broadened.

An operator with trusted database access can issue an independent credential for an existing user:

```sh
gitmorad issue-token --user-id <Gitmora-user-UUID> \
  --label 'Integration administration' \
  --scopes integration:read,integration:manage --ttl 720h
```

Its raw token is printed once to stdout, while only the hash is persisted. Store that output in your credential manager rather than logs or source control. Token scopes do not override project membership.

## Configure a repository

The repository's GitLab base URL and immutable project ID come from the catalog mapping established during bootstrap; the connection body cannot retarget those fields.

`PUT /v1/repositories/{repository-UUID}/gitlab` accepts strict JSON:

```json
{
  "api_token": "<GitLab API credential with read access to this project>",
  "webhook_mode": "disabled"
}
```

This explicit API-only mode needs no GitLab webhook or webhook secret. The worker verifies uploaded commit associations and collects MR/CI/deployment evidence through the existing periodic queue; manual sync can request an earlier check. Status reports `webhook_mode: "disabled"` with an empty `webhook_path`, and incoming webhooks are denied. Polling does not capture agent conversations: install the local collector/agent adapter separately. The human form defaults new connections to this mode.

To additionally enable signed webhooks, supply a matching credential instead:

```json
{
  "api_token": "<GitLab API credential with read access to this project>",
  "webhook_mode": "signature",
  "webhook_signing_token": "whsec_<base64 key copied from GitLab>"
}
```

Before saving, Gitmora calls `GET /api/v4/projects/{project-ID}` at the configured allowlisted base and checks its exact numeric ID. It then rechecks local administrator authority before committing the encrypted connection. The response contains connection health, generation, mode and webhook path; it never contains credentials. Repeating PUT verifies and replaces credentials, increments the generation, invalidates old worker leases and rechecks pending objects.

For GitLab versions supporting signing tokens, use the returned path as an HTTPS project webhook URL. Select relevant events in GitLab. Signing follows Standard Webhooks: HMAC-SHA256 over `{webhook-id}.{webhook-timestamp}.{raw-body}`, with the decoded `whsec_` key. Valid signatures must be within five minutes of the server clock; keep clocks synchronized. Multiple versioned signature candidates are supported. Reference: [GitLab signing tokens](https://docs.gitlab.com/user/project/integrations/webhooks/#signing-tokens).

An explicitly configured legacy connection instead supplies `legacy_webhook_token` with 16–256 printable characters and omits `webhook_signing_token`. It verifies `X-Gitlab-Token` in constant time. Signature mode never falls back to a legacy token. Legacy delivery identity prefers `webhook-id`, then `Idempotency-Key`, then `X-Gitlab-Event-UUID`; modern delivery IDs are preferable for retries.

Existing signed/legacy requests may omit `webhook_mode`; their supplied webhook credential retains its original meaning. If present, the mode must match that credential. Omitting both webhook secrets without explicit `disabled`, supplying a secret with `disabled`, or using an unknown mode is invalid. Replacing a connection changes its policy/credentials atomically and fences results from the previous generation.

## API operations

| Operation | Path | Result |
| --- | --- | --- |
| Configure/replace | `PUT /v1/repositories/{id}/gitlab` | Verified encrypted connection |
| Read health | `GET /v1/repositories/{id}/gitlab` | Enabled, generation, mode, error code, blocked state and verification timestamps |
| Explicit retry | `POST /v1/repositories/{id}/gitlab/sync` | 202 after durable scheduling; resets delayed missing-object checks |
| Disconnect | `DELETE /v1/repositories/{id}/gitlab` | 204; removes recoverable credentials and invalidates leases |
| Receive provider event | `POST /v1/integrations/gitlab/{id}/webhook` | 202 with `created`; provider authentication replaces Gitmora bearer authentication |
| Read MR evidence | `GET /v1/repositories/{id}/gitlab/merge-requests/{iid}` | Current immutable observation, diff identity, source commit page and completed result associations |
| Read CI | `GET /v1/repositories/{id}/gitlab/pipelines` | Bounded pipeline page including exact tested SHA and provider project |
| Read deployments | `GET /v1/repositories/{id}/gitlab/deployments` | Bounded deployed-SHA/environment page |

Configuration is bounded to 16 KiB and webhook bodies to 2 MiB. Authentication headers must not be duplicated. The minimal inbox keeps delivery/body digests, event kind and optional MR IID/after-SHA. Raw CI variables, messages and arbitrary provider metadata are not persisted. Matching retries do not duplicate work; changed content under the same delivery identity conflicts. Project identity is checked before acceptance. Webhooks insert only unconfirmed commit hints.

## Reconciliation and failure recovery

The durable connection queue is processed independently from the capture indexing outbox. Multiple workers use `FOR UPDATE SKIP LOCKED` leases. Every provider-result write checks the current connection generation, lease owner/expiry, enabled state and immutable catalog identity in a short transaction. No database transaction spans HTTP I/O. An old worker cannot write after token replacement, explicit requeue, lease expiry or disconnect.

Each batch checks access to the exact provider project, processes one fair metadata unit, then verifies at most 16 pending SHAs while its execution budget remains. Only an exact returned commit ID sets `confirmed_at`; webhook and client claims do not. The capture read endpoint's `confirmed_commits` then reflects that repository-scoped evidence. Commit lookup establishes observed object existence at verification time. MR state and completed lineage are separate API observations; none constitutes task acceptance or approval.

Further pending pages run immediately. Otherwise periodic verification runs every five minutes, so omitted webhook notifications do not strand uploaded commit associations. A 404 leaves the object unconfirmed and delays it for 15 minutes; a matching push hint or explicit sync retries sooner. Missing objects do not prevent other pending pages from progressing. Replacing credentials also resets pending retry times.

A lease lasts 60 seconds; provider work has a 45-second budget plus bounded completion cleanup. Transient errors retain work with exponential backoff capped at 15 minutes. A valid numeric `Retry-After` can extend rate-limit scheduling to one hour. Invalid credentials, inaccessible projects, forbidden destinations and encryption-key failures block the connection with a bounded health code. Replace credentials/configuration or explicitly request a retry after resolving the error.

## Metadata cursors and MR evidence

MR discovery, regular pipelines, child pipelines and deployments have separate persisted collection windows/pages. Each window uses fixed update-time bounds, ascending update order and a ten-minute overlap after the last completed watermark. Page data and cursor advancement commit together under the active connection lease. `X-Next-Page` and validated same-resource `Link` continuations are supported; missing pagination headers use a bounded page-size fallback. Returned links are never followed as destinations. A failed page retains its cursor.

Updated objects can move during offset pagination. A daily full repair scan and explicit full rescan on manual sync/credential replacement complement overlapping incremental windows. This is eventual reconciliation, not a claim that GitLab's paginated API supplies a transactional repository snapshot.

Webhook hints wake the relevant completed collection. A hint arriving during an active fixed window requests another window after the current one completes, preserving progress. MR events also queue their IID directly. Metadata units and MR detail/commit jobs share a persisted round-robin schedule. Connection health exposes each stream's watermark, active page, completion time, retry time and error code. A 404 collection remains explicitly unavailable and retries later; it does not become a successful empty snapshot or block independent commit verification.

MR list rows discover IIDs; the detail endpoint provides immutable sanitized observations. Descriptive changes keep the same code identity. The code key includes target/source projects, IID, head/base/start refs, state and merge/squash results. Source commit pages are bracketed by detail reads of that identity. A changed identity discards the fetched page and starts the replacement code version; old observations remain queryable. Preparing MRs with incomplete diff refs remain incomplete and retry on their own due time.

Lineage is materialized only when every guarded source page has completed for a merged code version. Only that version's source commits connect to its reported result SHAs. Historical discarded heads retain their own observations without acquiring the new squash relation. Fork source project IDs remain separate from the target; unknown/deleted source scope is represented by zero, never inferred as the target. MR source membership does not automatically confirm those source objects in the target repository. Reported result objects enter independent commit verification.

Pipeline IDs retain their tested SHA; deployment IDs retain their deployed SHA. A conflicting SHA under the same scoped identity is rejected. Old provider timestamps cannot replace newer projection state. `Pipeline.AppliesTo` requires an exact current MR head and source-project match; synthetic merge-result pipelines are retained as their own tested SHA and are not guessed to apply to a source head. CI status does not approve or merge anything.

MR reads accept `observation=<64-character digest>` for an exact historical observation, `after_sha=<full SHA>` and `limit=1..100` for source membership. `commits_complete` describes the whole guarded set, while `next_sha` indicates more stored rows. `result_shas` appears only for a completed merged set; these reported associations do not substitute for independent object confirmation. CI/deployment reads accept `after_id=<nonnegative ID>` and `limit=1..100`, returning `next_id` when another page exists. Reads require `integration:read` and current local project membership. Broader provider-permission freshness remains part of R03.

## Verification

`make verify` exercises real PostgreSQL authorization, migration, concurrency and existing capture/collector behavior. `make smoke-forge` runs the built API and worker against PostgreSQL and a deterministic loopback GitLab HTTP fixture, including API shutdown, independent worker processing and restart. Provider tests cover exact project/object IDs, redirects, allowlisting and rate limits. Lease, cross-repository SHA isolation and missing-object retry are separately covered.
