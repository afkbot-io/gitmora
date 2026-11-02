# Native API and MCP

Gitmora uses the developer's chosen external agent and model. The server stores, synchronizes and retrieves development evidence.

First enroll the CLI through the same browser account:

```sh
gitmora auth login --server https://gitmora.tasktopia.online
gitmora auth status --server https://gitmora.tasktopia.online
```

The credential belongs to that exact server and remains in the OS secret store. An explicitly set `WORKGRAPH_TOKEN` takes precedence for deliberate automation; it is never a command argument or an agent configuration value. Project membership and the key's capabilities still apply to every request.

## JSON API from the CLI

```sh
gitmora api get /v1/projects --server https://gitmora.tasktopia.online

gitmora api post /v1/context/search \
  --server https://gitmora.tasktopia.online --input query.json
```

`query.json` is a JSON object such as `{"project_id":"<project UUID>","text":"refreshToken"}`. Use `--input -` for stdin. `post`, `put`, `patch` and `delete` support the corresponding authorized core API operations; the same catalog, connection, source and credential contracts apply as in the web interface. The command does not expand the credential's authority.

Output is `{"status":200,"body":{...}}`; an empty successful response contains only `status`. Rejected requests exit unsuccessfully and report the HTTP status without copying private provider diagnostics. The client refuses redirects, non-application paths, ambiguous JSON and excessive input/output. Mutations are sent once. Keep the exact `request_id` and payload when recovering an uncertain result; do not create a fresh mutation automatically.

## Stdio MCP for a local agent

Configure the agent to launch:

```json
{
  "command": "/absolute/path/to/gitmora",
  "args": ["mcp", "--server", "https://gitmora.tasktopia.online"]
}
```

Wrap this command entry in the configuration shape your agent expects. No key belongs in the file. The process uses the saved account, forwards each request to the exact selected server and exposes only `context.browse`, `context.search`, `context.pack`, `context.expand`, `context.resolve` and `context.file`. It does not expose arbitrary command execution or workflow mutations. Use the explicit CLI API operation for authorized mutations.

Stdout carries MCP messages only. The server rechecks current credentials, membership and source availability on each call. Source text remains untrusted quoted evidence. Revocation or expiry prevents later calls; the bridge does not automatically open a login browser or mint a new key. Stop the agent before changing the selected server/account configuration.

To follow an MR/PR attached to an external task, call `context.resolve` with `project_id` and its human `url`, then pass the returned `query` to `context.pack`. No task tracker credentials are needed.

## Commit boundaries and recovery

CLI 0.6.0 records optional `commit_ranges` in version-1 snapshots. Codex frontiers store the normalized message endpoint of each observed Git transition; several tool calls in one captured window retain different endpoints. API/provider confirmation validates commit existence and PR membership, not semantic authorship of a message interval. Existing canonical revisions and continuation parent references remain immutable.

For a legacy conversation, use `gitmora recover-ranges --codex-thread NATIVE_THREAD_ID` from the repository after the conversation was captured. Other adapters may use `gitmora recover-ranges --session LAST_LOCAL_RECORDING_UUID`. The UUID is a Gitmora recording, not a native Codex thread ID. Recovery reads the local sanitized collector ledger and Git objects, never private native rollout files. It accepts successful recorded `git commit` output matched unambiguously to locally resolved full SHAs, establishes a conservative floor at the first known endpoint, and emits labelled `recovered_git_output` ranges between later endpoints. It preserves recorded checkpoints and queues new revisions under existing publication consent; it does not grant consent or send by itself. Run ordinary `gitmora sync` after the server supports migration 26. Missing, ambiguous or redirected-away output stays unavailable rather than being guessed from dates.

CLI 0.7.0 resolves recorded commit prefixes in a bounded local object batch rather than walking all history. Repositories with more than 2,000 commits are supported for recovery. Unresolved/quiet commit attempts break the recovered interval instead of being bridged into the next known commit. Explicit recovery rebuilds only `recovered_git_output` ranges; observed checkpoints win, and old immutable revisions remain retained. `ranges` reports intervals in changed windows, `updated_windows` counts those windows, and an unchanged replay returns `status: "unchanged"`. Agent disablement also blocks recovery selected by local UUID.

## Search and provenance

`context.search` accepts `agent`, `model`, `role`, repository-relative `file_path` and exact/abbreviated confirmed commits. A session UUID in `text` selects that Gitmora source under the chosen project. Pass returned `next` verbatim as `after` with unchanged filters; results contain one best chunk per source part. `context.expand` preserves metadata and exact citations. For a file's producer evidence, search its repository/full SHA/path with `kinds:["session","code_reference"]`; inspect `metadata.relation` and `metadata.model_basis` before describing authorship. [Examples and limits](retrieval.md#search-examples). The six read-only MCP tools and CLI credential scope stay the same.
