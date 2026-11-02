# Search and exact evidence

Search covers recorded agent messages, capture-reported file references and provider-verified retained code. It runs in PostgreSQL without a model. Code follows provider-reported changed paths at confirmed SHAs; it is not a full repository inventory.

Run migrations, the API and `gitmorad worker`. Capture events and provider code jobs feed a durable index queue. Provider metadata, code and indexing have separate durable progress. `worker --once` attempts one due provider metadata unit, one code unit and one index source. Retries are lease-fenced and idempotent.

Call `POST /v1/context/search` or MCP `context.search` with `project_id`, `text`, optional `repository_id`, `session_id`, full `commit_sha` or explicit `commit_prefix`, `file_path`, `agent`, `model`, `role`, `kinds`, `history`, `limit` and `after`. `kinds` accepts `session`, `code_reference`, `code`. Current project membership and the relevant `capture:read` or `integration:read` capability are required. API schemas are strict; removed task selectors are rejected.

Results contain exact evidence IDs, source identity/digest, part, `start`/`end` offsets, plain `content`, full-chunk digest, current/history flag, observation/index times and rank. Session sources identify repository/session/revision. Code identifies repository/SHA/path/blob/projection digest and line numbers. Source text is never rendered as HTML or treated as agent instructions. PostgreSQL rank is not a code quality score.

`current` means the latest observed session revision. Code is pinned to its SHA/path; it does not assert the current branch head. Disabled, denied or expired provider connections hide retained code. Ordinary session history remains readable under current Gitmora permissions without provider availability.

Use `context.expand` with an exact `evidence_id`, `offset` and `limit` to retrieve bounded additional text. Use `context.file` for a retained exact code file. All expansion repeats current authorization; old IDs are not capabilities. When quoting, retain the evidence identity, offsets and source SHA/revision.

For an MR/PR link use `context.resolve`, then `context.pack`. Packs expand verified continuation windows and source commit membership; SHA-scoped message search also requires recorded commit message ranges. A whole-session association never substitutes for a missing interval. See [context contracts](context.md), [code collection](code.md), [CLI/MCP](native-context.md) and [custom servers](custom-server.md).

`make smoke-context` verifies built API/PostgreSQL/search/MCP, replay, exact excerpts and revocation. `make smoke-agent-context` runs source discovery, pack retrieval and exact expansion through an actual local OpenCode MCP client with a deterministic model fixture. Load tests measure bounded synthetic workloads and are not a production capacity guarantee.


## How an agent searches

The server does not call a model. PostgreSQL uses a weighted `tsvector` and a GIN index with simple/English/Russian terms. Alternatives for each language are combined; words within each form use `plainto_tsquery` conjunction. Titles receive extra weight. A bounded message-wide vector also matches terms separated by chunk boundaries inside one message; the returned citation still identifies one exact chunk. This is lexical retrieval with stemming, not embeddings, typo correction or a guarantee that paraphrases match. The matching identity CTE is materialized independently, so GIN can select candidate IDs before expensive authorized-source joins; authorization and source availability still apply before ranking, LIMIT and text return. Exact empty-term evidence reads keep their direct plan.

A local agent chooses short queries, inspects the returned citations, narrows by repository/commit/session, then calls `context.expand` or `context.file`. For an MR/PR it should resolve the URL and page `context.pack` before making conclusions. Retrieved text is source material, not instructions. If the first query is empty, try related words or an identifier; do not claim that absent search results prove absent work. No server AI API or GPU is needed.

One best matching chunk per source part is returned, so one long tool record does not occupy a whole page. Search `next` is a query-bound keyset cursor: pass it verbatim as `after` while keeping filters unchanged. It pins an indexing cutoff and a resolved-prefix/text mode. Every page repeats current authorization. This is live source discovery, not a long-lived database snapshot; indexing or latest-revision changes can remove results, and a fresh search includes newer observations.

The web reader opens a hit at its exact revision and message position. It appends batches and verified continuations; “Conversation only” hides tools, and the local filter searches the parts already fetched. It does not claim to search unloaded parts. Raw records and immutable source identities remain available under source details.

For a repeatable read-only probe, set a short-lived scoped token in `GITMORA_BENCHMARK_TOKEN`, then run:

```sh
python3 scripts/benchmark-context.py --server https://gitmora.example.com \
  --project PROJECT_UUID --repository REPOSITORY_UUID --session SESSION_UUID \
  --query 'registration' --query 'notpresentxyzzzz' --samples 12
```

It reports sequential p50/p95 HTTP latency and response bytes, never response text or credentials. It does not follow redirects and accepts plain HTTP only on loopback. Run on the server to separate SQL/API time from network latency, and from a developer machine to measure the complete connection. This is a latency probe, not a team-capacity or concurrent-load claim.

## Search examples

The web field accepts text, a Gitmora session UUID, a full SHA, a unique abbreviation of at least seven hexadecimal characters, or a registered MR/PR URL. A short hex value with no available confirmed commit is searched as text; ambiguous commit abbreviations require more characters. Explicit `commit_prefix` requests never silently become text searches. A session UUID is scoped by project and may be narrowed to a repository. Native agent thread IDs and Gitmora session IDs are different identities.

Use these JSON objects with `POST /v1/context/search`, CLI `gitmora api post /v1/context/search --input query.json`, or MCP `context.search`:

```json
{"project_id":"PROJECT_UUID","text":"refreshToken","agent":"codex","role":"assistant","limit":20}
```

```json
{"project_id":"PROJECT_UUID","model":"model-id","limit":20}
```

```json
{"project_id":"PROJECT_UUID","repository_id":"REPOSITORY_UUID","commit_sha":"FULL_SHA","file_path":"src/auth.go","kinds":["session","code_reference"],"limit":20}
```

Agent/model filters are case-insensitive exact identifiers. A provider-qualified model such as `provider/model-id` also matches separately recorded provider/model metadata. `role` accepts `user`, `assistant`, `tool`, `system`. Path filters are exact repository-relative paths and require a repository. They match retained code, native tool path reports and reported file/line evidence; they do not invent a full repository inventory. Normal text terms use stemming, not fuzzy/paraphrase matching. For a literal hex string that also identifies a commit, quote it in the text query to suppress SHA auto-detection.

In the interface, less common filters are collapsed. Results append in pages of 20, open the exact source revision/message, and expose producer details without adding a separate settings page. A retained code result has a collapsed “Related changes” section using the same authorized search.

## Agent, model and file provenance

Search/expansion responses include optional source-reported `metadata`: integration name/version, model/provider, agent profile, message role/tool, native item/turn ID and a relation. `file_paths` returns at most eight complete paths and `file_path_count` reports the indexed path count. An explicit path filter returns that matching path. Metadata is derived from immutable sanitized source bytes; it does not grant authority or change evidence IDs.

- `model_basis=message_metadata`: the producer recorded this message's model. OpenCode preserves model/provider changes across assistant/tool records in the same chat; user prompts are not assigned an executing model.
- `model_basis=session_configuration`: a thread or capture configuration was reported. The current/latest Codex thread model is **not** historical per-turn execution telemetry; Gitmora does not assign it to all earlier messages. Unknown models stay absent from search metadata.
- `model_basis=import_report`: a compatibility source reported the model. Imported Git AI line ranges retain `attribution_kind`, `reported_line_start/end` and `reported_commit`; they remain claims about that exact target SHA.
- `relation=tool_reported`: a successful native editing tool reported the path in this message and the query's commit is inside its recorded message interval. The original tool record/diff is readable at the cited revision and position.
- `relation=git_ai_reported`: an imported source reported line attribution. Provider confirmation establishes commit/file identity, not the truth of the attribution claim.
- `relation=session_reported`: a session mentioned a path; it does not establish which tool, commit delta or lines produced it. `retained_code` means provider-verified pinned file text and carries no inferred model attribution.

A changed path plus a final provider blob is not proof that every final line was generated by that model. Shell-only file writes, unsupported tools, concurrent manual edits and rebases without retained source evidence leave gaps. The code view shows recorded links, never a fabricated AI percentage. Native path metadata is bounded to 256 retained paths per record; original exposed tool text and existing source limits still apply.

Server migration 27 adds rebuildable metadata/path/message-vector indexes. A projection version leases old completed sources for atomic rebuilding while their acknowledged text remains readable. Pending counts include rebuilds. No canonical capture/digest or publication permission changes. Upgrade the server before collecting with CLI 0.8.0. Existing native window partitions remain stable when metadata is added. Continuation parent revisions are frozen once a child pins them: metadata-only replay of older windows keeps those canonical bytes; the active window can be enriched in a new revision. Older sealed windows can therefore retain unknown metadata rather than breaking their references. New collector fields require the local schema-7 capability floor: stop older collectors/watchers and back up private SQLite state first. Old executable processes must not remain running during the upgrade.
