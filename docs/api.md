# Implemented API v1

The [OIDC/browser-session contract](identity.md) documents `/v1/auth/*`, explicit operator identity binding, same-origin CSRF and raw bearer/MCP separation. Existing keys remain api-kind credentials; browser cookies are a separate authority transport.

The additive `POST /v1/context/pack` route and `context.pack` MCP tool are documented in [agent context](context.md), including independent permissions, task/session/MR selectors, exact-head CI, continuation/MR pagination and explicit completeness limits.

`POST /v1/context/browse` and read-only `context.browse` discover authorized cached task/session-window/MR headers with stable typed pagination. The [work navigation contract](work-navigation.md) describes independent capabilities, availability gates, decimal cursors and human task/session/MR workflows.

## Authentication and isolation

Send `Authorization: Bearer <Gitmora token>`. Tokens have independent `capture:read` and `capture:write` capabilities, expiration and revocation. Repository membership is checked inside each persistence transaction; a token capability does not override a membership role. Readers cannot upload. User disablement and revocation are observed on subsequent authentication; an already accepted in-flight request may finish.

Raw token values are shown once by trusted offline bootstrap or `issue-token` operations. The database stores SHA-256 bearer-token hashes. Provider credentials use a separate scoped encryption store. Provider administration requires explicit `integration:read`/`integration:manage` capabilities in addition to project membership.

## Routes

| Method/path | Capability | Behavior |
| --- | --- | --- |
| `GET /health/live` | Public | Process liveness, `200` |
| `GET /health/ready` | Public | Schema/migration compatibility, `200` or `503` |
| `POST /v1/captures` | `capture:write` | `201` for a committed new revision; `200` for identical retry |
| `GET /v1/repositories/{repository UUID}/sessions/{session UUID}` | `capture:read` | Latest authorized snapshot, digest and independently confirmed commit list |
| Same GET with `?revision=N` | `capture:read` | Exact positive revision; zero selects latest |

Capture reads and writes use `Cache-Control: no-store`. Error bodies have a stable shape: `{"error":{"code":"conflict","message":"..."}}`. Infrastructure error details, credentials and raw evidence are not returned.

## Capture request

`Content-Type: application/json`; maximum raw body 3 MiB, canonical snapshot 2 MiB. JSON must have exact lowercase field names, no unknown fields or duplicate keys, valid UTF-8, one root object and no trailing value. Structural nesting is bounded.

```json
{
  "schema_version": 1,
  "repository_id": "11111111-1111-4111-8111-111111111111",
  "session_id": "22222222-2222-4222-8222-222222222222",
  "revision": 1,
  "agent": {"name": "local-agent", "version": "1", "model": "local-model"},
  "started_at": "2026-10-30T10:00:00Z",
  "ended_at": "2026-10-30T10:01:00Z",
  "task": {
    "country_id": "33333333-3333-4333-8333-333333333333",
    "task_id": "44444444-4444-4444-8444-444444444444"
  },
  "messages": [{"role": "user", "text": "Preserve retry state"}],
  "files": ["src/queue.go"],
  "commits": ["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"]
}
```

`task`, `ended_at` and agent `version` are optional. Revision is 1–1,000,000,000. Message roles are `system`, `user`, `assistant`, `tool`; tool messages require `tool_name`. Limits: 5,000 messages, 128 KiB per message text, 5,000 normalized relative file paths, 2,000 full lowercase SHA-1/SHA-256 commit IDs. Git internals and parent/absolute paths are rejected. Name/model/version and tool-name bounds are defined by the domain types.

The server sanitizes recognized credential patterns, canonicalizes timestamps to UTC and sorts/deduplicates file/commit associations before hashing. Message order is preserved. The returned digest identifies stored sanitized content; it may differ from a client's raw input digest.

Session identity fixes the originator, start time, integration name and task binding. Start a new session when switching task bindings. Complete snapshots may arrive out of order: latest means the largest accepted revision. Reusing an existing revision for changed content returns `409`. Revisions never overwrite earlier evidence.

Optional `continuation` fixes an exact previous session/revision/digest, root UUID, segment number and bounded reason as part of the child's immutable identity. Children can arrive before parents. A read reports `parent_reference_verified: true` only after matching that edge's bytes, owner, source/task scope and sequence; it does not verify the entire ancestor graph or a summary's correctness. Messages may carry `kind: agent_summary` only with the assistant role; unknown kinds are rejected. Legacy snapshots without these fields keep their canonical representation. See [native continuation](native-continuation.md) for exact fields, budgets and deployment order.

## Status and retry semantics

- `400`: malformed or ambiguous request/query.
- `401`: missing, malformed, expired, revoked or unknown token.
- `403`: missing capability or repository authority; resource existence is not disclosed.
- `404`: no matching session/revision inside an authorized repository.
- `409`: conflicting revision or changed immutable session identity; manual investigation is required.
- `413` / `415`: request size / content type violation.
- `422`: domain contract violation.
- `500` / `503`: failed operation; retry the same immutable revision with bounded backoff.

An acknowledged write includes the revision and its indexing outbox event in the same transaction. A pending outbox event is not evidence that indexing has run. Uploaded commit SHAs begin unconfirmed. The independently running [GitLab worker](gitlab.md) confirms exact object existence through the configured project's API; webhook/client claims alone cannot do so. Separate authorized metadata routes expose immutable MR observations, CI/deployment evidence and completed squash lineage. None substitutes for human task acceptance.


Native agents can use [CLI API access and the stdio MCP bridge](native-context.md) with the same account.
