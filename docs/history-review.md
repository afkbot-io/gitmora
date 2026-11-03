# Conversation and search review — 2026-11-01

The 0.5.2 reader keeps a full-width workspace and a readable conversation column. Storage windows are grouped only along server-verified continuation chains. Requests, answers, tools and exact provenance retain their original order and identity. The reader appends batches, preserves scroll and open disclosures, and opens a search result at its recorded revision/message. A loaded-part filter is explicitly local. No server model is introduced.

## Competitor findings

- [Entire CLI search](https://github.com/entireio/cli/blob/d7012996f21878bcdc42f10b2a282e628d0671ee/cmd/entire/cli/search_cmd.go) advertises hybrid semantic/keyword search across checkpoints, sessions and commits. Its [search client](https://github.com/entireio/cli/blob/d7012996f21878bcdc42f10b2a282e628d0671ee/cmd/entire/cli/search/search.go) exposes BM25, ANN and reranking metadata and calls a remote semantic-search service. This is evidence about its public client contract; the closed backend implementation was not independently audited. Its [session workflow](https://entire.com/blog/sharing-sessions-is-the-new-social-coding) emphasizes reading the relevant prompts/checkpoints rather than every tool line. Gitmora adopts compact reading and precise source navigation; it does not claim Entire's semantic backend.
- [Git AI](https://github.com/git-ai-project/git-ai/blob/8a6184a3a8fc4e997ccfff3b16d79688e4255af4/README.md) links generated lines to agents and prompts. The checked [dispatcher](https://github.com/git-ai-project/git-ai/blob/8a6184a3a8fc4e997ccfff3b16d79688e4255af4/src/commands/git_ai_handlers.rs) exposes `blame` and `show-prompt`. Its bundled search skill still mentions `search`/`continue`, but these are absent from that dispatcher; do not copy those instructions as verified current commands. The useful product pattern is code → commit → prompt, complemented in Gitmora by MR URL → confirmed source commits → verified dialogue.
- Gitmora originally used local-agent reasoning over deterministic lexical results and exact expansion. Optional [local semantic retrieval](semantic-search.md) now adds embedding-based paraphrase matching alongside lexical search. Model quality and capacity still require evaluation against the installation's corpus.

## Measured baseline

Read-only production loopback measurements, 12 sequential samples per case, before the SQL change:

| Case | p50 | Largest observed sample |
| --- | ---: | ---: |
| Russian search | 1536 ms | 2386 ms |
| Common identifier (`token`) | 2054 ms | 3394 ms |
| No match | 1495 ms | 1599 ms |
| First session revision response | 69 ms | 91 ms |

The session response was 1,035,142 uncompressed bytes. Message batches limit rendering; the existing revision endpoint still transfers one entire bounded revision. Continuations are fetched on demand. Markdown is lazy-loaded (roughly 54 kB gzip for the reader chunk), so the public landing does not download it immediately. These figures exclude browser rendering, TLS and network transit and are not a concurrency/capacity claim.

`EXPLAIN ANALYZE` identified a join plan reading 27,303 lexical vectors instead of selecting GIN matches first. Materializing matching IDs lets PostgreSQL use `retrieval_chunks_lexical`; the same no-match plan fell from 1516 ms to 4.4 ms. Membership, capabilities, provider freshness and confirmed-commit predicates still precede ranking/LIMIT and source text return. The release is also verified through built API/MCP and the PostgreSQL integration suite. Reproduce HTTP measurements using [the latency probe](retrieval.md#how-an-agent-searches); workload and cache state affect results.

## Storage

At the baseline the complete production database occupied 274.5 MiB, including all projects and retained code:

| Storage | Size |
| --- | ---: |
| Capture revisions, including indexes | 18.4 MiB |
| Canonical revision bytes before database compression | 43.9 MiB |
| Search chunks, vectors and indexes | 234.2 MiB |
| Retained code snapshots, including indexes | 8.0 MiB |

There were 53 stored capture revisions and about 34,800 search chunks. Revisions and search projections are intentionally separate: a source can have multiple immutable revisions, and long tool outputs produce multiple indexed chunks. Database allocated size also includes page overhead and free space. These values exclude backups, images and release archives. The local checkout additionally had about 96 MiB of collector state and 3.9 GiB of ignored build/test artifacts; those are development files, not the deployed platform database. No history, backup or retained source was deleted to improve the numbers.

For larger teams, monitor database/table/index growth separately, keep bounded tool capture, and define an explicit archival policy before removing old revisions. Automatic destructive retention is not enabled by this change.

## Commit-scoped context (0.6.0)

The earlier shared-root expansion was too broad for PR navigation. Full conversation identity is not a commit boundary. [Entire metadata](https://github.com/entireio/cli/blob/d7012996f21878bcdc42f10b2a282e628d0671ee/api/checkpoint/metadata.go) stores checkpoint transcript offsets separately from the complete transcript. Its [architecture](https://github.com/entireio/cli/blob/d7012996f21878bcdc42f10b2a282e628d0671ee/docs/architecture/sessions-and-checkpoints.md) explains offset advancement and bounded overlap. [Git AI's standard](https://github.com/git-ai-project/git-ai/blob/8a6184a3a8fc4e997ccfff3b16d79688e4255af4/specs/git_ai_standard_v3.0.0.md) associates commit line ranges with prompt records; prompt context can legitimately be reused across commits. Neither model makes wall-clock intervals or a session UUID sufficient to prove semantic ownership.

Gitmora 0.6.0 stores half-open message intervals per observed commit, preserves full history separately, and limits PR/MCP reading to the intervals of confirmed source commits. Multiple commits in one capture window retain their own tool endpoints. Partial commits and rewritten identities can carry earlier context explicitly. Legacy recovery uses successful recorded Git output and locally verified objects, labels the result `recovered_git_output`, and leaves missing/ambiguous boundaries unavailable. A real-data copy test for PR #4 recovered 181 messages in two intervals (parts 32–33), instead of exposing the complete 8,233-message conversation. This is checkpoint context, not proof of individual line authorship.
