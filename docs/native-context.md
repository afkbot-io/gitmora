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
