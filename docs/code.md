# Verified GitLab code evidence

Gitmora retains bounded text files from provider-reported changed paths of independently confirmed commits. This extends session/task retrieval with exact repository/commit/path/blob provenance. It is an incremental evidence collection, not a complete clone or inventory of every file in a repository.

## Ingestion and verification

Use the existing encrypted [GitLab connection](gitlab.md), its exact destination allowlist and a token that can read the repository files and commit APIs. Run migrations, then the API and worker. A separate code lane shares the connection authority but has its own durable lease, retry schedule and health; code failures do not block ordinary MR/CI reconciliation. `worker --once` attempts one due GitLab metadata unit, one code unit, and one index source.

Provider confirmation creates one durable code run per repository/commit in the same transaction. Migration backfills runs for already confirmed commits. Each claim reads one diff page or one file, alternating discovery and file work. No provider request holds a database transaction. Completion checks connection generation, lease owner/expiry, current catalog tuple and run version/page. Rotation, disablement and explicit retry fence stale workers.

The worker enumerates commit diff pages of at most 50 paths, with a local limit of 10,000 paths per commit. GitLab also applies its own diff limits: reaching the final API page does not establish complete changed-file coverage. `discovery_ended` and `local_path_limit_reached` describe these distinct conditions. Deleted files, symlinks, submodules and unsupported modes are recorded as skipped. Invalid paths and paths containing recognized secrets are represented only by a hashed placeholder and a skip reason.

For a regular file, HEAD metadata must match the full requested SHA and exact path. Files above 512 KiB are skipped before body download. Raw bytes are fetched at that SHA with `lfs=false`; the worker checks size and the Git blob object hash before retaining text. SHA-1 and SHA-256 object formats are supported. It excludes binary/non-UTF-8/control-bearing data and LFS pointers. The raw bytes are not persisted. GitLab's JSON file endpoint can convert encodings, which is why verification uses its raw endpoint instead. See the official [repository files API](https://docs.gitlab.com/api/repository_files/) and [commit diff limits](https://docs.gitlab.com/api/commits/#retrieve-commit-diff).

Recognized credentials are redacted before persistence with `code-lines-v1`. Replacement preserves newline count, including multiline private keys, so original line numbers remain usable. Rune offsets refer to sanitized text and can differ from raw offsets. The immutable projection records raw SHA-256, provider/blob identity, policy and a separate digest of the complete sanitized model. This is known-pattern redaction, not a guarantee that arbitrary secrets are detected.

Snapshots and index jobs commit atomically. A conflicting projection for the same repository/commit/path is rejected rather than overwritten. Indexing validates the full source digest and persists bounded cited chunks. A worker restart resumes durable discovery, file and index work. A missing file retries after 15 minutes; a transient file failure backs off that path while other paths progress. A missing permission probe hides that commit's files and schedules rediscovery. Rate limits delay the repository lane. Administrative retry rechecks files without deleting immutable snapshots.

## Availability and authorization

Code requires `integration:read` and current project membership. A capture-only token cannot read it. Search applies source eligibility before ranking/LIMIT; exact citation expansion and direct file quotes recheck it. Current catalog provider URL/project ID must match the snapshot. Disabled or denied connections hide retained code.

Successful file reads renew a repository-level source-access observation. When the queue is idle, a stored file is probed every five minutes; reads require a successful observation within ten minutes. Expiry hides code until revalidation succeeds. Credential rotation clears that observation immediately. This is shared service-account repository authority; individual GitLab user permissions are not synchronized. Detection is periodic, not instantaneous. This lane also respects the main GitLab connection's blocked state.

## API and MCP

| Route | Capability | Result |
| --- | --- | --- |
| `GET /v1/repositories/{repository}/code/commits/{full SHA}` | `integration:read` | Discovery, stored/pending/skipped/missing counts, reasons and safe health |
| `POST /v1/repositories/{repository}/code/commits/{full SHA}/sync` | `integration:manage`, administrator | Fence current work and schedule retry; `202` |
| `POST /v1/repositories/{repository}/code/file` | `integration:read` | Bounded exact-file quote |

The file quote request uses strict JSON:

```json
{
  "commit_sha": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "file_path": "src/auth.go",
  "offset": 0,
  "limit": 1200
}
```

The JSON envelope is bounded to 8 KiB. `offset` counts Unicode runes in the sanitized whole file; `limit` defaults to 1,200 and cannot exceed 2,048. Responses include the exact identity, projection/raw digests, redaction provenance, observation time, rune range, original line range and optional `next_offset`.

The read-only MCP tool `context.file` takes these fields plus `repository_id`. It returns the same bounded evidence. `context.search` supports kind `code` and an optional exact `file_path` filter with `repository_id` and `commit_sha`. Its chunks and expansions include line numbers and immutable code source identity. `code_reference` remains an agent-reported path, a different evidence type.

For code, `current=true` means that this exact pinned source is available. It says nothing about the current branch or MR head. A file being present at a commit does not establish authorship, successful tests, deployment or task acceptance. Missing search results do not prove file absence: consult ingestion status and account for provider limits and skipped files. MR/head/CI packs and native agent context are described in [context](context.md); server-generated answers are outside this product. Collection is bounded and does not promise a complete repository/tree inventory.
