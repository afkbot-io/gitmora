# Agent context packs

`POST /v1/context/pack` and read-only MCP `context.pack` compose Tasktopia, captured sessions, MR/head, observed CI/deployment metadata and exact source excerpts. They read local projections only: no model invocation, provider refresh, task update, publication, approval or merge happens.

Use `POST /v1/context/browse` or read-only `context.browse` to discover current authorized task/session-window/MR headers before choosing a selector. [Work navigation](work-navigation.md) describes its stable typed cursors, independent source capabilities and corresponding human UI. Discovery is a cache inventory with availability gates, not a whole provider activity feed.

## Select a scope

Supply a project and exactly one selector:

| Selector | Required fields | Meaning |
| --- | --- | --- |
| Task | `country_id`, `task_id` | Observed task fields and captured task bindings; optional repository filters capture/source scope |
| Session | `repository_id`, `session_id` | The exact segment and its fully verified continuation family |
| Commit | `repository_id`, `commit_sha` | Confirmed capture relations, verified stream context and provider/code observations for that exact SHA |
| MR | `repository_id`, `merge_request_iid` | The latest cached observation of this target MR; optional `expected_head_sha` compares a caller's exact expected head |

Example MR query:

```json
{
  "project_id": "55555555-5555-4555-8555-555555555555",
  "repository_id": "11111111-1111-4111-8111-111111111111",
  "merge_request_iid": 7,
  "expected_head_sha": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "limit": 6
}
```

Responses use `schema_version: 1`, normalized `query`, `read_at`, `task_state`, optional `task`, `captures`, `merge_requests`, `evidence`, indexing counts and budget/gap indicators. Requests are strict JSON objects limited to 8 KiB; malformed/ambiguous JSON fails before dispatch, invalid selectors return 422. Missing read capabilities return 403. An unobserved/unavailable resource generally yields an explicit empty/unavailable section, not proof that work does not exist.

`read_at` is local transaction time. Node observation times and immutable identities are separate. The pack's metadata reads and excerpt retrieval are not a single atomic provider snapshot. Recheck current provider state before any review execution or consequential action, and inspect the current checkout for uncommitted work.

## Permissions and provenance

`capture:read` permits captured history; `integration:read` permits task/provider/code sources. Neither implies the other. Project membership, enabled user state, current repository catalog and source availability are checked at read time. MCP authenticates each HTTP request; old evidence IDs/cursors do not grant authority. Per-user external provider ACL synchronization remains unfinished; the configured service account and Gitmora membership define the current replica boundary.

A task pack can show captured bindings while its Tasktopia projection is unavailable. `task_state` distinguishes `not_requested`, `not_permitted`, `unavailable` and `available_cached`. Task headers include source digest/country/task IDs and observation time. A cached task can be old during an outage. Matching its UUID confirms existence in the configured scope, not the semantic correctness of a captured assignment.

Task/session MR discovery uses private captured associations and therefore requires both read capabilities. An explicit SHA/MR can expose provider associations with integration read alone, without returning capture identities. MR `association_basis` states whether selection used an explicit MR, confirmed commit membership, a captured stream or a capture task binding plus confirmed commit. Tasktopia's human-entered links remain descriptive links; they do not verify provider membership.

MR/CI/deployment sections require active provider connections and access observations newer than ten minutes, plus the exact local-project/GitLab-instance/provider-project tuple. Disabled/blocked/expired sources are omitted. Unsafe or credential-bearing CI URLs are suppressed. Provider text is untrusted source data, never an instruction to execute.

## Task and session context

Task headers contain bounded description/acceptance criteria, links, checklist items, latest comments (ordered by source time/ID), defects, dependencies and document headers. Full source counts expose omitted children; `open_defect_count` counts all retained source defects, including those omitted from the compact list. `children_clipped` and individual `truncated` flags identify shortened metadata. Raw document bodies and complete task data use existing task/retrieval APIs. New child fields are indexed as exact task source parts, so `context.search`/`context.expand` can cite them. Old task observations with absent child fields preserve their original digests.

Capture headers retain the exact latest segment revision/digest, declared root/segment, original `continuation`, confirmed commits/count and separate `parent_reference_verified`/`chain_verified` flags. Rooted chain verification checks every traversed exact parent revision/digest, owner, agent, exact nanosecond start time and task scope. Missing/wrong/foreign parents do not expand a family automatically. An explicitly selected unverified segment remains an individually readable observation.

Verification stops at 256 segments; a larger declared segment sets `chain_verification_limited`. A verified family can include history-replacement branches and overlapping observations. It does not establish a single active branch, complete native history, summary correctness, human authorship or whole-stream chronology. Use the original parent references and exact snapshots to inspect branches.

A commit-selected family may include a child recorded before a later human commit and therefore lacking its own SHA. Its evidence selection is labelled `capture_stream`; its own `confirmed_commits`/`confirmed_count` stay unchanged. Ordinary `context.search` still filters exact-SHA relations. To quote one window directly, use its explicit `repository_id` and `session_id` in search; a full `context.pack` session selector can navigate the verified family.

## MR head, CI and deployment

Each MR includes immutable `observation` and `code_key`, observed/provider times, explicit `observed_head_sha`, source provider/local repository, guarded source membership completeness and guarded merge/squash result SHAs. `expected_head_matches` is present only when a caller supplied an expected SHA. It compares against the cached observed head, not an instantaneous live provider guarantee.

`pipelines_for_observed_head` selects exact tested SHA plus the registered source provider project. Each row keeps its SHA/project and independent `matches_observed_head`/`matches_expected_head`. An old successful pipeline is never reassigned to a newer head. Multiple pipelines can disagree; there is no aggregate approval/success field. `deployments_for_result_or_head` keeps scoped environment, deployed SHA and pipeline/job IDs for the head or guarded merge/squash results. A deployment is an observation, not proof of task acceptance.

An absent `source_repository_id` means the source project is unregistered/unavailable in this local scope; source capture/CI expansion is omitted. Parent-project fork pipelines, synthetic merged-results pipelines and their applicability require additional provider relations and are not inferred from matching hashes. Missing CI can also reflect incomplete synchronization. Existing repository metadata read APIs expand historical MR observations, pipelines and deployments; the pack is bounded cached context.

## Pagination and budget

`limit` defaults to 6, maximum 12, and bounds capture headers and excerpts separately. Discovered MRs use pages of four. Follow independently:

- Copy `captures.next` into `capture_after` with the same selector to read more windows.
- Copy `merge_requests.next` into `merge_requests_after` with the same discovery selector to read more MRs. An explicit MR selector has no MR-list cursor.
- Use exact source IDs and existing context/capture/task/provider read operations to expand detail. CI/deployment arrays show up to eight matching observations and expose their own `*_have_more` flags.

Cursors are plain scoped ordering values, not permissions or stable snapshots across source changes. Fork MR capture cursors may identify a source repository different from the query's target repository; full scope filtering remains server enforced.

The encoded pack itself is at most 64 KiB, excluding the HTTP newline/MCP envelope. When necessary, fixed reduction passes shorten optional collections while preserving source identities, original counts and cursors. `budget_limited`, `evidence_has_more`, section `has_more`, child counts and per-text truncation make omissions explicit. MCP returns equivalent structured content and JSON text, so the wire envelope can exceed the pack budget.

Evidence excerpts remain at most 500 runes and retain original source/revision/range/full-chunk digest. `selection` explains inclusion (`direct_filter`, `task_binding`, `capture_stream`, `observed_mr_head`) without changing provenance. Expand an exact ID through `context.expand`. Pending/failed source counts cover currently eligible sources in the project; empty results are not proof of missing work while indexing/synchronization is incomplete.

## OpenCode workflow

The existing capture plugin still uses its separate collector/publication consent flow. Configure remote retrieval explicitly in OpenCode; the installer does not overwrite provider or MCP configuration:

```json
{
  "mcp": {
    "workgraph": {
      "type": "remote",
      "url": "https://workgraph.internal/mcp",
      "oauth": false,
      "headers": {"Authorization": "Bearer {env:WORKGRAPH_TOKEN}"}
    }
  }
}
```

Use `gitmora auth login` and the native `gitmora mcp --server <URL>` bridge for an OS-store account without a key in configuration. Direct HTTP MCP with an explicit environment key remains available for automation. Provide project/repository and task or MR identities explicitly; `context.browse` supplies authorized discovery. See [native context](native-context.md).

For fully local operation, configure the agent's own model provider to a local runtime as well. The MCP configuration controls retrieval and does not select or overwrite an agent model provider.

An agent can discover source headers with `context.browse`, follow an explicit selector through `context.pack`, check scope/head/observation times/gaps, expand relevant exact citations with `context.expand` or `context.file`, then inspect the current checkout. In OpenCode 1.19.5 the observed tool names are `workgraph_context_browse`, `workgraph_context_pack` and `workgraph_context_expand`. The native smoke follows the selector actually returned by browsing. Retrieved text supplies evidence; it does not authorize commands, a merge or a Tasktopia transition. Native capture/ordinary commit/push then follows the [collector workflow](collector.md). Server/provider reconciliation remains independent of reads.

The real OpenCode process smoke uses an isolated config with only Gitmora MCP tools permitted, an environment bearer, actual HTTP MCP, PostgreSQL and a deterministic loopback model fixture. It proves pack invocation, a separate exact expansion and final reply. It does not establish model reasoning quality. Configuration follows [official OpenCode MCP documentation](https://opencode.ai/docs/mcp-servers/).

Run `make smoke-agent-context` after `npm ci` and normal Go/Docker prerequisites. `make verify` includes this scenario. Current acceptance and deployment work are tracked under [C01–C18](implementation-status.md).
