# Tasktopia connection and evidence publication

The implemented connector reads task context through Tasktopia HTTP MCP and publishes a separate provenance document. It uses an explicit country UUID and task UUID, independent of the country selected in Tasktopia's UI. Task numbers alone are insufficient.

## Server configuration

Configure both `gitmorad serve` and `gitmorad worker` with the shared `WORKGRAPH_ENCRYPTION_KEYS` and `WORKGRAPH_ENCRYPTION_ACTIVE_KEY` described in `gitlab.md`, plus:

```sh
export WORKGRAPH_TASKTOPIA_ALLOWLIST='https://tasktopia.internal/mcp'
```

The allowlist contains exact endpoint URLs; HTTPS is required except for loopback HTTP test endpoints. Redirects, ambient HTTP proxies and alternate destinations are disabled. The worker may run Tasktopia alone, GitLab alone or both integrations, alongside source indexing. `worker --once` attempts one due Tasktopia unit, one GitLab metadata unit and one independent code unit when configured, plus one source-index job. `migrate` must run before the updated API/worker starts.

Create a dedicated Tasktopia service account with access to the required country. Its MCP token needs `country:read,tasks:read` for connection verification and context reads, and `tasks:write` for document publication. Tasktopia token creation revokes the identity's previous active MCP tokens: use a dedicated identity rather than rotating a developer's personal key.

Configure a project with `PUT /v1/projects/{project}/tasktopia`, an administrator membership and `integration:manage`:

```json
{
  "endpoint": "https://tasktopia.internal/mcp",
  "country_id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
  "api_token": "<dedicated Tasktopia MCP token>",
  "auto_link_merge_requests": false
}
```

Gitmora first authorizes the administrator, verifies country visibility through MCP, encrypts the token and rechecks administrator authority while saving. Endpoint and country are immutable after initial connection, including while disabled. Repeating PUT with the same scope rotates credentials, applies the explicit automatic-link policy, clears blocked health and retries unfinished reparable jobs. Omitting `auto_link_merge_requests` selects false. Permanently invalid provider links remain blocked until new valid provider evidence is available. GET returns health and policy without the token. DELETE destroys the envelope and prevents future worker claims; an external request already in flight may still have completed.

## Reading a task

Accepted captures normalize their immutable country/task binding in the same database transaction. When that country matches a connected Gitmora project, discovery automatically schedules `task.get` for the explicit UUID. Captures can arrive before the connection, or the connection before captures; migration backfill and periodic repair cover both orders. Discovery does not parse task titles, branch names or conversation instructions. A client binding identifies the requested task; it is not independent proof of task acceptance.

An administrator can also schedule `POST /v1/projects/{project}/tasktopia/tasks/{task UUID}/sync`. The worker reads `task.get` with the configured country and the explicit UUID. `GET` of the same path returns the current sanitized task, observation time and digest to members with `integration:read`. Each successfully observed distinct sanitized model remains in immutable history.

Known credential forms, sensitive URL query parameters and URL fragments are removed before persistence. Source responses, fields, document/link counts and retained projections have explicit limits. An excessive task fails with integration health rather than silently truncating requirements. Ordinary refreshes recur every five minutes. This interval is not a freshness guarantee during outages; consumers must inspect `observed_at`.

Local Gitmora project membership authorizes the shared service-account projection. Individual Tasktopia user roles are not synchronized yet. A detected service credential/country denial blocks the connection and hides cached task reads. A task-not-found result invalidates that task. Other transient outages retain the last observation with its original timestamp. Permission freshness remains part of R03 and final acceptance.

## Publishing evidence

An administrator with `integration:manage` **and** `capture:read` sends:

```http
POST /v1/projects/{project}/tasktopia/publications
Content-Type: application/json
```

```json
{
  "repository_id": "<Gitmora repository UUID>",
  "session_id": "<stored session UUID>",
  "revision": 1
}
```

The exact revision must belong to a repository in that project, have a task binding in the connected country, and contain at least one independently provider-confirmed commit. A client claim alone fails. The response contains the immutable report and publication ID, making the queued external write inspectable.

The worker checks current task access, reconciles an already existing identical report, then calls `task.document_upsert` using `workgraph:publication:<UUID>`. Reports contain repository/session/revision identifiers, the exact capture digest, country/task identifiers and confirmed SHA references. They exclude conversation text. Reports list up to 256 confirmed SHAs and explicitly mark larger sets as a compact excerpt; the exact revision remains stored in Gitmora. They make no test, acceptance, deployment or completion claim.

`GET /v1/projects/{project}/tasktopia/publications/{publication}` requires `integration:read` and `capture:read`. It returns `pending`, `blocked` or `complete`, a bounded error code, the exact document and completion time. Repeating the original publication request returns its existing journal entry and payload. Completion means that this external document operation was acknowledged or reconciled; it is unrelated to task completion.

Network failure after a remote commit retains the same job and external idempotency key. An identical existing dedicated document completes reconciliation without another mutation. Different existing content blocks the job. Resolve that conflict deliberately; the worker does not overwrite content observed to have changed. There is no remote compare-and-set guarantee against an edit racing after the read.

## Automatic MR links

An administrator may set `auto_link_merge_requests: true` in the connection PUT. This is persistent authorization for the server to append verified MR URLs to matching bound tasks. It needs `tasks:write` on the dedicated Tasktopia token. It does not authorize changes to task status, progress, acceptance, labels, documents or existing links. Evidence-document publication remains a separate explicit operation.

The worker discovers one due project per pass, repairing at least once per minute; capture acceptance, commit confirmation and completed MR source pages wake it earlier. Each discovery transaction creates at most 100 new task reads and 100 link intents. Backlogs remain due for subsequent passes. Discovery commits separately from operation claims and locks the connection before jobs. Canonical conversations are not scanned on every poll.

A candidate requires an independently confirmed captured SHA, the matching country/task binding, current catalog scope, active source and target GitLab connections with successful synchronization newer than ten minutes, an available current MR observation and a complete guarded source commit set. For a fork, both registered repositories must belong to this Gitmora project and GitLab instance, and the MR's explicit source project must match the captured repository. An identical SHA in a different source project is insufficient. Confirmed merged result SHAs also match their exact target MR after guarded membership completes.

The provider's human URL must match its GitLab origin/base path and exact IID, with no credential/query/fragment data. The outbound title is a stable `GitLab MR !<IID>` rather than the provider's descriptive title. Each country/task/MR URL has a durable operation ID and stable `workgraph:mr-link:<UUID>` idempotency key. Immutable association observations retain source repository/session/revision/digest/SHA and the MR observation/code identity. New eligible source observations can support the same outbound intent without changing its external payload.

Before claiming an intent, the store checks the current association again. A retired/incomplete/unavailable relation blocks delivery with `association_unavailable`; later eligible observations can revive it. Credential replacement, policy changes, expiry and disconnect fence stale local completions. The exact leased task/kind/payload/read timestamp are also checked. Provider I/O holds no database transaction; a disable or head change racing an already dispatched external request cannot retract that request. Links already delivered remain historical and are not removed after rebase/head changes.

The worker first reads the actual task. An existing equivalent URL completes readback without changing its human label. Otherwise it calls `task.link_add`, then reads the task back and indexes the updated observation. An ambiguous network failure retains the same operation; a subsequent read detects a previously committed link. A concurrent duplicate conflict is reconciled through readback. An acknowledged write whose link is absent on readback blocks as `link_readback_mismatch`, preserving a potential human edit. Completed intents do not automatically recreate a manually removed link.

`GET /v1/projects/{project}/tasktopia/tasks/{task UUID}/links` returns the latest 50 delivery records and `has_more`, including pending/blocked/complete state, safe diagnostic, URL and completion time. `integration:read` and current project membership are required. Exact capture association evidence is included only when the token also has `capture:read`. This is operation history, not a declaration that the MR still applies to the current task or head.

The [human workspace](web.md) now implements connection verification/rotation, explicit automatic-link policy, current health, task-UUID refresh and disconnect. `make smoke-settings-tasktopia` verifies these forms against actual isolated Tasktopia MCP/source. Advanced publication/link job controls and journal navigation are outside the core interface; task/session/MR context is available through the work views. Read-only search/expansion is available through [Gitmora MCP](retrieval.md). Ordinary push delivers capture evidence and can eventually trigger discovery/link delivery under the configured policy; it does not close a task or automatically publish an evidence document.

## Verification

The normal `make verify` includes domain, HTTP, application and real PostgreSQL task-integration tests plus actual MCP Go client/server transport contracts. It uses no production provider.

To exercise the actual local Tasktopia source handler, with Node 24+:

```sh
TASKTOPIA_SOURCE='/absolute/path/to/Tasktopia' \
TASKTOPIA_NODE='/absolute/path/to/node24/bin/node' \
make smoke-tasktopia
```

The harness copies source, migrations, two required asset manifests and pinned npm metadata into ignored `build/tasktopia-contract`. It installs dependencies with scripts disabled, creates isolated schemas in the dedicated Compose PostgreSQL and starts the real Tasktopia MCP/auth/application implementation. It copies no `.env`, uploads or existing database. Synthetic credentials remain in process memory. Cleanup stops both APIs and drops the known schemas.

The harness now also runs the GitLab HTTP fixture with the built provider worker. Without seeding confirmation or MR tables, capture acceptance discovers the task, the worker verifies commit/MR membership, the administrator enables automatic links and the worker appends a real Tasktopia MR link. API restart and uncertain-ACK replay leave exactly one remote link mutation. Task status/progress remain unchanged. This verifies actual-source protocol interoperability, automatic discovery/link delivery, document publication and revocation; it does not certify a production deployment, all future versions, per-user source ACL synchronization or a full platform rollout.
