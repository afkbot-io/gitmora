# Agent context

Gitmora exposes six read-only MCP tools and matching JSON APIs. These read stored authorized observations; reading does not invoke a model or mutate a provider.

| MCP tool | JSON API | Purpose |
| --- | --- | --- |
| `context.browse` | `POST /v1/context/browse` | Discover session or MR/PR headers with typed pagination |
| `context.resolve` | `POST /v1/context/resolve` | Resolve a synced MR/PR URL inside one project |
| `context.pack` | `POST /v1/context/pack` | Read related sessions, commits, MR/PR and CI context |
| `context.search` | `POST /v1/context/search` | Search recorded text, file references and retained code |
| `context.expand` | `POST /v1/context/expand` | Expand an exact evidence ID with bounded offsets |
| `context.file` | `POST /v1/context/file` | Read an authorized retained file at a verified commit |

## From an external task to code history

Keep task management in your existing system. Copy the attached MR/PR link and call:

```json
{"project_id":"55555555-5555-4555-8555-555555555555","url":"https://github.com/team/repository/pull/7"}
```

Send this to `context.resolve`, then pass the returned `query` to `context.pack`. Resolution matches trusted cached provider observations in the chosen project. It never fetches the supplied URL. A URL that has not synced or whose connection is disabled/stale returns not found. Custom GitLab/GitHub Enterprise URLs, installation prefixes and ports use the same approved provider tuple as the connection.

## Pack selectors

Supply `project_id` and exactly one selector:

| Selector | Fields |
| --- | --- |
| Session | `repository_id`, `session_id` |
| Commit | `repository_id`, full `commit_sha` |
| MR/PR | target `repository_id`, `merge_request_iid`; optional `expected_head_sha` |

A pack includes `schema_version: 1`, normalized `query`, `read_at`, `captures`, `merge_requests`, `evidence`, indexing counts and bounded-gap flags. `limit` defaults to 6, maximum 12. Copy `captures.next` into `capture_after`, and `merge_requests.next` into `merge_requests_after` for discovered MR lists. These are separate cursors. The encoded response is bounded; respect budget and truncation flags and expand exact sources separately.

For a complete verified MR source commit set, capture selection includes all confirmed source commits, not only the head. Fork history requires a separately registered source repository within the same local project and provider instance. Incomplete membership falls back to the exact observed head. Unconfirmed client SHA declarations never establish provider membership. `source_commits` lists up to 50 cached source SHAs; `source_commit_count` and `source_commits_more` expose larger sets. Capture selection uses the complete verified set even when that display list is clipped.

Full-dialogue continuation expansion verifies exact parent revision/digest, owner, agent, original start time and immutable legacy source scope. PR/commit selection includes only windows with recorded intervals for the selected confirmed commits; legacy broad links are marked `range_status: unavailable` and return no transcript text. Missing or foreign parents do not expand a family. Traversal stops at 256 segments and reports its limit. Stored transcripts are observations, not proof that every reported tool succeeded or that a summary is correct.

`capture:read` permits sessions and their excerpts. `integration:read` permits provider observations and retained code. Current membership and enabled user state are checked for every request. Provider reads additionally require an active connection and access observation newer than ten minutes. Removing membership or disabling a source revokes reads even with old evidence IDs/cursors. The configured service account and Gitmora membership define the replica boundary; per-user external provider ACL mirroring is not implemented.

`expected_head_matches` compares a caller’s expected SHA to the cached head. CI rows remain pinned to their source project and tested SHA; old successful CI is never assigned to a newer head. These observations do not approve a review or merge. Inspect the current checkout and provider before consequential work.

Connect using [native CLI/MCP](native-context.md) or [your own domain](custom-server.md). See [search](retrieval.md) and [workspace](work-navigation.md) for details.


## PR-scoped messages (0.6.0)

Resolve a PR URL, call `context.pack`, choose a capture family and call the same tool with the original PR query plus `dialogue_id` (`root_session_id` only when `chain_verified`, otherwise the exact window `source.session_id`). Keep `merge_request_iid` and the target `repository_id`; switching to a standalone `session_id` explicitly requests full history. `captures.message_ranges` are half-open original message positions, not semantic or line-authorship proof. `basis` distinguishes observed checkpoints, partial-commit carry-forward and recovery from recorded Git output. `ranges_more` bounds metadata only; transcript reading uses the complete retained intervals.

Read `transcript.items`, retaining each exact source/revision/digest, original `index`, `message_offset` and `commit_shas`. Pass the entire `transcript.next` as `message_after`. Pages contain at most 20 message entries and a bounded text/JSON budget. Long messages continue at a Unicode-rune `rune_offset`; concatenate fragments only for the same source/index in order. The cursor pins source bytes plus MR head and commit-set scope; a changed revision/head/base/commit-set mode requires reopening the context. Every page rechecks membership, source capability and provider freshness.

`scope.missing_commit_ranges` means some selected commits have no recorded message intervals. It does not imply that the work is absent or AI-authored. Do not fill gaps with the whole conversation. Full history uses a separate explicit `session_id` query and does not enumerate other PRs. Commit-only navigation lists only MR memberships of that exact SHA. No new model or autonomous server agent is introduced.

For compact conversation reading, set `message_view: "compact"`. Long tool entries carry `text_preview: true` and `full_text_after`; their text is a shortened display value, not the exact original. Read the original with the same selector and `message_view: "full"`, passing `full_text_after` as `message_after`. Follow its Unicode fragment cursor until that original message is complete. Every call repeats current authorization, selected commit membership and revision/head checks. Normal user/agent messages stay complete and use normal pagination. The web reader uses compact pages and loads full tool records when expanded.
