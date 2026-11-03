# Search use cases and acceptance

The current direction is a self-hosted development-history system, closer to
[Entire's searchable session/commit history](https://entire.com/blog/introducing-agentic-search-for-code-and-context)
than to native per-line AI blame. Search returns evidence; a developer's local
agent can interpret it. No server agent, generated answer or task-tracker integration
is required. Optional [semantic retrieval](semantic-search.md) adds paraphrase
matching while preserving exact source identity and access checks.

| Use case | Retrieval path | Checked boundary |
| --- | --- | --- |
| Understand a decision | Text search → exact source expansion | Lexical identifiers and Russian/English paraphrases; expansion equals the retained original. |
| Continue in a new chat | Local MCP search → bounded pack → source excerpts | Actual stdio broker and API share authorization; sessions have immutable revision/digest references. |
| Review a PR against its intent | Resolve MR/PR URL → pack its confirmed commits and message intervals | A shared chat does not bring earlier/later PR conversations into the selected PR. Missing ranges remain explicit. |
| Investigate a regression | Exact SHA/prefix → recorded commit interval → related messages/files | A text similarity score never broadens exact commit membership; ambiguous SHA is rejected. |
| Open context from a task's MR link | The task system supplies the MR URL; the same resolver/pack flow follows | No task API token or duplicate task synchronization is needed. The MR must belong to a registered repository. |
| Identify the producer | Agent/model/provider/role/path filters and citation metadata | Two reported models in one chat remain distinct; unknown historical models are not guessed. File references do not establish final-line authorship. |

## Verification layers

`scripts/test-integration.sh` includes PostgreSQL tests for multiple commit windows,
model/path filters, query-bound pages, permission revocation, stale leases,
failed model calls, projection replacement and exact original expansion. Synthetic
vectors check the contract independently of a particular model's ranking.

`make smoke-context` checks the built API, CLI and native MCP against PostgreSQL,
including authorization revocation. The existing context/range tests cover PR/commit
windows and verified continuations. `make smoke-semantic` additionally uses a real
locally provisioned CPU model and a disposable schema: eight authored source records,
four labeled positive questions, an unrelated question, CLI search, exact expansion,
commit-scoped retrieval and native MCP revocation. No production data is written.

`scripts/evaluate-search.py` compares lexical/hybrid Recall@k and MRR on operator
labels, with p50/p95 timing and explicit pending/degraded markers. It never records
transcripts or credentials in its report. A small fixture establishes working
plumbing and representative examples; it is not a quality or capacity guarantee
for every company corpus. Re-evaluate new languages, model versions and large imports.

## What remains outside this first stage

Semantic indexing currently covers recorded messages and retained changed code;
it is not a complete repository/symbol index. MR titles and arbitrary commit
messages are not yet independent semantic source types. Exact MR links and SHAs
continue to resolve through provider metadata. Hybrid lanes are bounded and may
omit low-ranked matches; use lexical mode for a larger exact-word traversal.

Native line attribution, token/cost accounting and per-message timestamps require
additional source-supported telemetry. Existing agent/model/provider/profile,
native item/turn IDs, tool path reports and imported Git AI attribution retain their
reported basis. The system does not infer which model authored every final line.

Next steps toward Entire-like discovery are separately indexed commit/PR metadata,
larger real-corpus relevance evaluations, measured profile-specific ANN indexes and
better local-agent handoff commands. Code hosting and autonomous server review are
outside the current search-focused scope.
