# Work navigation

“Работа” in the [human workspace](web.md) connects saved task, agent-session and Merge Request observations. It reads the existing authorized projections and exact source APIs. It performs no provider mutation, model inference, command execution, approval, merge or task completion.

## Human flow

Select a project and source kind. Tasktopia tasks are project/country scoped. Sessions and MRs also support a repository filter. Lists show twenty records per page and expose “Загрузить ещё источники”; ordering is by stable identity, not inferred activity time. A session row is one accepted window, with its latest revision. Opening it expands only verified continuation relationships through the existing context pack. Agent-declared roots/task bindings remain observations.

The selected task shows its current cached header, description, acceptance criteria and bounded checklist/comments/defects/dependencies/document headers. Counts/truncation remain visible. A dependency opens its exact country/task ID; a missing cached dependency is an explicit gap. External links are inert text unless they have a safe HTTP(S) URL; opening one is a separate action.

A selected session first shows its sanitized transcript. Choose an exact revision or request the latest accepted revision. Messages page in groups of twenty. The view distinguishes agent-reported commits/paths from independently confirmed GitLab commits, and never treats a stored tool reply as successful execution. Transcript and related context are separate authorized reads, with their own identities/times. Exact evidence quotes open through the existing citation API.

MR context shows its observed head, provider observation/digest, association basis, source project, exact-head CI, applicable deployment observations and explicitly confirmed result SHAs. Refresh preserves the originally expected head. A change is a warning; “Открыть наблюдаемый head” intentionally selects the new expectation. A successful pipeline for an old/different SHA cannot become current-head CI. Missing checks are not success.

Changing project, source kind, repository or selected source clears/cancels prior details. A bounded in-memory back history returns to earlier selections. On subsequent reads, removed membership or blocked/unavailable sources stop returning their content. Existing login/key/session expiration and revocation rules still apply. Already-rendered DOM is not continuously reauthorized. Source navigation/transcripts are not written to app browser storage.

## Discovery API and agent tool

`POST /v1/context/browse` accepts strict JSON, at most 8 KiB:

```json
{
  "project_id": "11111111-1111-4111-8111-111111111111",
  "kind": "session",
  "repository_id": "22222222-2222-4222-8222-222222222222",
  "limit": 20
}
```

`kind` is `task`, `session` or `merge_request`. Limit defaults to twenty, maximum fifty. Tasks reject a repository filter. Responses contain `schema_version:1`, normalized `query`, transaction `read_at`, bounded `items` and optional `next`. Each item has a source title/state/time, exact identity cursor and a `query` suitable for `context.pack`; task headers carry a decimal-text `number`, while session headers also carry agent/window/revision/digest. MR queries pin the observed expected head when present.

Send the returned `next` unchanged as `after` with the original filters:

| Kind | Cursor | Ordering | Independent capability |
| --- | --- | --- | --- |
| `task` | `{ "id": "<task UUID>" }` | Task UUID | `integration:read` |
| `session` | `{ "repository_id": "<UUID>", "id": "<session UUID>" }` | Repository UUID, session UUID | `capture:read` |
| `merge_request` | `{ "repository_id": "<UUID>", "iid": "7" }` | Repository UUID, numeric IID | `integration:read` |

IID cursor text is canonical positive int64 decimal, preserving values above JavaScript's exact integer range. Cursor shapes cannot be mixed. A cursor grants no access: current enabled account/project membership, repository ownership and source capability/availability are reapplied before ordering and LIMIT. Task sources require an enabled, unblocked country connection and available current task. MR discovery requires an enabled/unblocked GitLab connection checked within the existing ten-minute observation window. The API does not contact providers; update connection health/sync separately. Invalid JSON is 400; invalid typed scope/cursor is 422; missing authority is 403; an unavailable use case is 503.

The additive read-only MCP `context.browse` tool has the same arguments, policy, deadlines and `structuredContent.result` envelope. Agents can discover a source, pass its explicit query to `context.pack`, then use `context.expand` for an exact citation. MCP remains bearer-only and reauthenticates every call. A capture-only key cannot list task/MR headers; an integration-only key cannot list session headers. Provider MR discovery itself does not reveal capture-derived associations.

Older context-pack numeric IDs outside JavaScript's safe integer range are labelled rather than shown/requested as a rounded identity. Browser navigation rejects unsafe MR IDs/cursors; new browse cursors and task header numbers remain decimal text. This is an explicit UI compatibility limit, not full large-int support throughout existing protocols.

## Verification and boundaries

`make smoke-work` uses actual Chromium, built API/PostgreSQL and a signed local OIDC provider with synthetic accepted source. `python3 scripts/smoke-web.py --work --serve` keeps this isolated fixture for headed Playwright CLI inspection. `make smoke-work-tasktopia` additionally needs the actual local Tasktopia source through `TASKTOPIA_SOURCE` and Node 24+ through `TASKTOPIA_NODE`. It uses an isolated source/schema copy and controlled GitLab HTTP provider; no original `.env`, account data or database are copied. Successful source data is not browser-mocked.

`make smoke-agent-context` drives actual OpenCode through discovery, context pack and exact expansion, using a deterministic local model fixture. This proves native tool interoperability, not model quality. Current acceptance, source coverage and deployment work are tracked under [C01–C18](implementation-status.md).
