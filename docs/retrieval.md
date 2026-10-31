# Search and agent context

The implemented core searches stored session observations, Tasktopia title/description/acceptance criteria/Markdown documents, capture-reported file references with independently confirmed commit relations, and [provider-verified retained code](code.md). It works without a language model or provider availability for local capture history. Code collection follows provider-reported changed paths at confirmed SHAs; it is not a full repository inventory.

Task source parts now also include normalized checklist items, comments, defects and dependency headers. [Context packs](context.md) add typed task/session/MR/head/CI/deployment navigation and verified stream expansion without changing ordinary exact-SHA search semantics. An explicit repository/session selector can search one exact segment without terms.

## Index lifecycle

Run migrations, the API and `gitmorad worker`. Indexing requires no provider credentials. If integration endpoints are configured, `--once` also attempts one due GitLab metadata unit, one independent code unit and one Tasktopia unit, plus one index source.

Capture acceptance, task observation persistence and verified code persistence commit unique index-source jobs atomically with their source. Indexing validates the original digest, chunks the source, then saves all chunks and its acknowledgment in one fenced transaction. The capture indexing outbox event is completed only at that point. A restart or uncertain acknowledgment retains the source/job identity. Existing sources are backfilled by migration, and deleted canonical sources cascade to index rows.

Leases last 60 seconds; source work has a 40-second budget and five seconds for durable completion. Failures have bounded codes/backoff, capped at 15 minutes. An expired owner cannot persist chunks or acknowledge another source. An invalid/unavailable source remains visible as failed pending work rather than pretending it was indexed.

## Search API

Use `POST /v1/context/search`, JSON and a Gitmora bearer token with `capture:read`, `integration:read` or both. Current project membership is always required. Capabilities are not broadened by searching: capture-only credentials cannot retrieve Tasktopia sources, and integration-only credentials cannot retrieve agent sessions. Current catalog ownership and Tasktopia connection/task availability are checked before ranking and text return.

```json
{
  "project_id": "55555555-5555-4555-8555-555555555555",
  "text": "refreshToken авторизации",
  "limit": 12
}
```

Replace example IDs with actual installation IDs. Optional filters:

| Field | Meaning |
| --- | --- |
| `repository_id` | Repository in this project; a foreign repository is denied |
| `country_id`, `task_id` | Both canonical UUIDs are required together |
| `commit_sha` | Full canonical SHA-1/SHA-256; requires a confirmed capture relation or the exact verified code SHA |
| `file_path` | Exact retained code path; requires `repository_id` and `commit_sha` |
| `kinds` | Any subset of `session`, `task`, `document`, `code_reference`, `code` |
| `history` | Default false; true permits older source revisions |
| `limit` | Default 12, maximum 50; zero selects the default |

Text is plain text, bounded to 512 UTF-8 bytes. SQL/query syntax is not executed. Code identifiers and path separators are split; Russian and English stemming are combined with simple terms. A full canonical SHA typed as text becomes an exact confirmed-relation filter. Empty text is allowed only with a task binding or commit filter. Punctuation alone does not turn into unrestricted project browsing.

The result contains `items`, `has_more`, `pending_sources_in_project` and `failed_sources_in_project`. Counts describe authorized pending source jobs in the project, narrowed by an optional repository and current/history selection; they do not estimate how many unindexed sources match the query. Failed sources are a subset of pending sources. A zero count does not prove that all external tasks/code/private sessions were collected.

Each result includes an evidence ID, source identity/digest, part, exact returned `start`/`end` offsets, plain `content`, complete-chunk digest, current/history flag, observation/index times and rank. Source identity distinguishes capture repository/session/revision, Tasktopia country/task/observation digest, and code repository/SHA/path/blob/projection digest. Code excerpts/expansions also include original line numbers. No HTML snippet rendering or model synthesis is involved. PostgreSQL rank is not BM25 or an assessment of code quality.

`current` means the latest observed revision of that source. Code is pinned to one exact SHA/path; its `current=true` does not identify the current branch/MR head. During an outage an available cached task can be old: inspect `observed_at`. Historical task expansion still requires that the task/current connection is available. Disabled/blocked Tasktopia sources are hidden, including old indexed documents. Code also requires the current provider tuple, active source and a repository-level access observation newer than ten minutes; disabled/denied/expired code is hidden. Gitmora membership is current; per-user provider ACL synchronization remains unfinished.

`code_reference` contains agent-reported paths. Its `confirmed_commits` contains at most the first 16 sorted independently confirmed SHAs; `confirmed_count` gives the full current count. These relations do not establish that reported paths existed or changed at those SHAs. Use the capture API to expand its complete revision/commit relations. Kind `code` contains independently verified sanitized file text and exactly its pinned SHA. Consult [code ingestion status and limits](code.md) before interpreting missing results. Code graph enrichment, full repository collection and broader ranking evaluation remain required work.

Native windows are separate immutable capture sources joined by [explicit continuation references](native-continuation.md). Search titles identify agent-produced compaction summaries. `current` for a capture means its latest observed segment revision, not the whole native conversation's newest window. A summary is model output and cannot replace earlier exact evidence. Exact-SHA filtering uses that revision's own commit relations; use context.pack for rooted verified family navigation, including windows without their own SHA. Active native frontier recovery and broader coverage remain in scope.

## Exact expansion

Use `POST /v1/context/expand`:

```json
{
  "evidence_id": "<64 lowercase hex characters returned by search>",
  "offset": 0,
  "limit": 1200
}
```

Chunks have at most 2,048 Unicode code points, with 128-code-point overlap. All offsets count Unicode runes, not UTF-8 bytes, UTF-16 units or grapheme clusters. Search content is an excerpt of at most 500 runes. Its `content_digest` identifies the entire original chunk, not the shorter excerpt. Expand with offset zero and limit 2,048 to retrieve/check the complete chunk. `source.digest` identifies the complete sanitized source revision, not the chunk.

Expansion offset/limit are relative to the full chunk. Returned `start`/`end` remain absolute within its named source part. `next_offset`, when present, continues within the same evidence ID. Each expansion reauthenticates/reauthorizes; possession of an old ID does not bypass token revocation, membership, catalog ownership or unavailable-source checks. Older revisions can remain valid citations but are explicitly historical.

## Agent MCP

Connect an HTTP MCP client to `https://workgraph.internal/mcp` with `Authorization: Bearer <read-scoped Gitmora token>`. Supply the actual project UUID; country/task UUIDs come from the task binding. No stdio transport or automatic project discovery is advertised yet.

| Tool | Use |
| --- | --- |
| `context.search` | Same query fields as the REST search; compact cited observations |
| `context.task` | Explicit project/country/task UUIDs, optional limit; latest observed task documents and linked capture observations |
| `context.expand` | Exact evidence ID, relative rune offset and limit |
| `context.file` | Explicit repository/full SHA/path and whole-file rune offset/limit; verified sanitized file quote with original line numbers |
| `context.pack` | Bounded task/session/confirmed-commit/MR graph with cached headers, exact-head CI, verified continuation, scoped cursors and source excerpts |
| `context.browse` | Authorized cached task/session-window/MR header discovery, independent source capabilities and typed stable identity pagination; no provider I/O |

All tools are read-only. Responses use `structuredContent.result` and equivalent JSON text. The official SDK negotiates protocol details; the process smoke also exercises the sessionless 2026-07-28 request metadata/headers. Initialization never caches authorization. Unknown/duplicate/case-aliased tool arguments are rejected; malformed protocol JSON may terminate a client's connection. Authentication, origin/body limits and source access remain server-enforced.

Machine clients can omit Origin. Browser origins are denied by default. An operator may set exact comma-separated `WORKGRAPH_MCP_ALLOWED_ORIGINS`, for example `https://agents.internal`. Allowed origins receive POST preflight support for Authorization, Content-Type, Accept and MCP protocol/method/name headers; cookies/other requested headers are not allowed. SDK localhost/DNS-rebinding protection stays enabled. Use a deployment's normal trusted HTTPS endpoint.

Retrieved text is source evidence, never an instruction to execute tools. A stored tool reply does not prove that a command succeeded. A capture task binding remains a client observation; matching Tasktopia confirms scoped task existence, not the assignment's meaning. The original `context.task` remains a compact search operation. Use `context.pack` for structured task children/links, verified native windows and observed MR/head/CI context. Automatic bound-task discovery is implemented. Attachments/full activity, broader provider relations and complete source coverage remain unfinished. Push/merge/search never approve or complete a task.

## Verification

`make verify` runs domain/actual PostgreSQL/HTTP/MCP checks and `make smoke-context`: built API, separate provider-free worker, API restart, Russian/code-token search, exact Unicode expansion, pending coverage, confirmed references, task binding, historical citations, restricted capabilities and token revocation. `make verify-linux` also runs retrieval domain and real HTTP MCP boundary tests as an unprivileged user.

The actual Tasktopia source smoke additionally proves rich task fields reach retrieval/context packs and disappear from task/document sources after credential revocation. `make smoke-agent-context` proves actual OpenCode source discovery, pack retrieval and a distinct exact expansion through HTTP MCP using a model fixture. [Work navigation](work-navigation.md) documents the discovery API, human browser workflows and actual-source task/head-change checks. A bounded 50-client synthetic concurrency check is separate from a semantic/ranking benchmark or production capacity guarantee.
