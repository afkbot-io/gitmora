# Search and exact evidence

Search covers recorded agent messages, capture-reported file references and provider-verified retained code. It runs in PostgreSQL without a model. Code follows provider-reported changed paths at confirmed SHAs; it is not a full repository inventory.

Run migrations, the API and `gitmorad worker`. Capture events and provider code jobs feed a durable index queue. Provider metadata, code and indexing have separate durable progress. `worker --once` attempts one due provider metadata unit, one code unit and one index source. Retries are lease-fenced and idempotent.

Call `POST /v1/context/search` or MCP `context.search` with `project_id`, `text`, optional `repository_id`, `session_id`, full `commit_sha`, `kinds`, `history` and `limit`. `kinds` accepts `session`, `code_reference`, `code`. Current project membership and the relevant `capture:read` or `integration:read` capability are required. API schemas are strict; removed task selectors are rejected.

Results contain exact evidence IDs, source identity/digest, part, `start`/`end` offsets, plain `content`, full-chunk digest, current/history flag, observation/index times and rank. Session sources identify repository/session/revision. Code identifies repository/SHA/path/blob/projection digest and line numbers. Source text is never rendered as HTML or treated as agent instructions. PostgreSQL rank is not a code quality score.

`current` means the latest observed session revision. Code is pinned to its SHA/path; it does not assert the current branch head. Disabled, denied or expired provider connections hide retained code. Ordinary session history remains readable under current Gitmora permissions without provider availability.

Use `context.expand` with an exact `evidence_id`, `offset` and `limit` to retrieve bounded additional text. Use `context.file` for a retained exact code file. All expansion repeats current authorization; old IDs are not capabilities. When quoting, retain the evidence identity, offsets and source SHA/revision.

For an MR/PR link use `context.resolve`, then `context.pack`. Packs expand verified continuation windows and source commit membership; ordinary exact-SHA search keeps direct SHA relations. See [context contracts](context.md), [code collection](code.md), [CLI/MCP](native-context.md) and [custom servers](custom-server.md).

`make smoke-context` verifies built API/PostgreSQL/search/MCP, replay, exact excerpts and revocation. `make smoke-agent-context` runs source discovery, pack retrieval and exact expansion through an actual local OpenCode MCP client with a deterministic model fixture. Load tests measure bounded synthetic workloads and are not a production capacity guarantee.
