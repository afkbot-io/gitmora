# Local collector and Git integration

The implemented collector accepts normalized observations, stores them locally and delivers context through normal Git hooks. Native Codex and OpenCode adapters are included; OpenCode has been exercised with 1.19.5. Explicit [native CLI login](cli-auth.md) uses browser OIDC consent and the OS secret store. Additional native integrations require their own acceptance checks.

## Setup and normal use

Build both binaries with `make build`. Place `gitmora` at a stable executable path before enabling hooks. Use `gitmora auth login --server <URL>` for the native secret-store workflow, or provide the scoped token through the process environment using your secret manager; the default variable is `WORKGRAPH_TOKEN`. Tokens are not accepted as CLI arguments or saved in repository configuration.

```sh
gitmora init --server https://context.internal --repository-id <Gitmora-repository-UUID>
gitmora capture < observation.json
git add src/queue.go
git commit -m 'Preserve failed deliveries'
git push origin main
gitmora status
```

`--remote` defaults to `origin`. Setup binds the immutable Gitmora repository UUID, API URL, named remote and its exact push URL. Identical setup is idempotent; changed identity/configuration is rejected instead of silently retargeting queued data. The server repository UUID is obtained from initial setup/catalog administration, not inferred from a task number.

There is no task-tracker binding command. Projects and repositories scope new recordings; immutable task namespaces from earlier versions are retained only for historical compatibility. A shared native conversation may cover several commits/PRs. Recorded checkpoint positions determine the transcript intervals shown for each change; a conversation UUID or commit date does not prove semantic ownership. See [Codex](codex.md) and [context reads](context.md).

## Native OpenCode

After repository initialization, enable the plugin and restart OpenCode:

```sh
gitmora agent enable opencode
```

The plugin is installed under `$XDG_CONFIG_HOME/opencode`, or `~/.config/opencode` when XDG is unset, in `plugins/workgraph.ts` and a private `workgraph/` helper directory. It embeds no provider and needs no npm installation. Provider/model settings remain the user's OpenCode configuration. The installation stores an absolute collector executable path; keep the binary at that path. `--config-dir /absolute/path` selects a different OpenCode configuration directory for either enable or disable.

This is a global plugin that activates only in explicitly initialized Git repositories. Its status probe does not create collector files in other repositories. User text, completed assistant text, completed/error tool results and scoped changed paths are captured; reasoning parts and attachment/data URLs are excluded. Large individual text/tool results carry an explicit omission marker. Native history is acquired in validated same-session keyset pages and split into bounded windows. Failed/incomplete acquisition records no partial pass; excessive inputs fail explicitly and preserve prior evidence. Native pagination is pinned to OpenCode 1.19.5; its source limits/codec are documented in [native windows](native-continuation.md).

Awaited tool callbacks record a before/after HEAD window. A tool that writes files, commits several times and pushes in one command is therefore linked retrospectively even if Git hooks ran before OpenCode persisted its tool result. Fast-forward windows retain intermediate commits; pre-tool local ref tips exclude pre-existing history after checkout; new rewritten commits remain eligible for context. This is observed session/commit context, not proof of individual line authorship. A separate test exercises agent edits followed by a developer's ordinary commit and push.

Final conversation updates after a push become eligible only for that already authorized recording. The same native source keeps its original recording identity across restart. Other uncommitted conversations stay private. Durable native file-event identities prevent replayed history from reintroducing paths already consumed by an earlier commit.

The adapter serializes/coalesces lifecycle notifications, re-reads current SDK state and drains pending work on plugin disposal. It sends only a stable complete prefix: an unfinished parallel tool blocks later parts, avoiding insertion into already recorded evidence. Each capture attempts bounded delivery of eligible data. Configure `gitmora sync --watch` for continuing offline recovery after the native process exits.

```sh
gitmora agent disable opencode
```

Disable removes only verified owned files. Existing `opencode.json` and unrelated plugins remain. Modified files or replaced owned directories are conflicts and are preserved. Repository-wide collection/delivery consent is controlled separately with `gitmora disable` and `gitmora enable`.

The `workgraph-opencode/4` adapter supports [native windows and continuation](native-continuation.md), including completed compaction summaries, retained pruned results, history replacements and conversations beyond one snapshot's 2 MiB limit. Original task/publication scope and immutable evidence remain intact. Durable native tool boundaries recover observed intervals after process termination; unknown ends require explicit resolution. Richer attachments and broader native version/platform coverage remain in scope.

## Durable native tool recovery

The plugin persists a tool's initial HEAD, pre-existing local commit tips and original source scope before reading SDK history, and its first observed end before waiting for any queued history read. Source windows, exact target segment and normalized message endpoint are accepted atomically. Known endpoints are applied in source order after restart; a later checkpoint cannot consume earlier unprocessed context. Startup applies target-bound entries without SDK access; completed unbound entries recapture their original source. Applying resolves only the stored interval and marks association/completion together. Later human HEAD changes and callback retries cannot extend an already observed end. Git operations concurrent inside the observed interval are context, not proven authorship.

Inspect current-worktree metadata with `gitmora frontier list`, or one exact entry with `gitmora frontier get --id <hash>`. A null `after` means the process never observed the ending boundary. The collector leaves it unresolved; verify the actual Git history and choose an explicit end, or close the intent:

An exact checkpoint also waits while another unfinished tool from the same native source lacks a verified message position. Its order is unknown, so automatically consuming that context would be unsafe. Finish source recovery or explicitly resolve/abandon the pending intent; other native sources remain independent.

```sh
gitmora frontier resolve --id <hash> --end <verified-full-SHA> --reason 'Verified actual tool commit'
gitmora frontier abandon --id <hash> --reason 'Tool did not execute'
```

Restart OpenCode after resolution to recapture the source and associate the stored interval. If `target_session_id` already exists, `gitmora frontier apply --id <hash> --sync` can finish without source access. `--sync` attempts delivery of already eligible data for at most two seconds; failed delivery retains the outbox. These commands grant no publication permission. Operator reasons are bounded and known-secret patterns are redacted.

Adapter authors can use `frontier begin` and `frontier end` with strict stdin JSON `{ "agent": "opencode", "source_session_id": "opencode:<native-ID>", "call_id": "<stable-tool-ID>" }`. Begin/end read actual HEAD themselves. Match that tool to its exposed native result. Use `capture --pin-source --frontier-at <hash>:<message-end>` on the window containing that result, then `frontier apply`. The endpoint is a count of normalized stored messages, including the result; it is not a raw native-item index. Repeat the flag for multiple tools in a window. `--frontier` remains a legacy broad relation without exact ranges; never substitute the last window for a missing tool result. Mixing `--frontier` with `--observe-head-from`, another source/worktree or an unfinished tool is rejected. An unchanged observation replay still binds its original accepted segment. These local metadata commands require repository initialization and never need provider credentials.

The private SQLite ledger now stores bounded native source locator values for recovery, while mapping/call/event keys stay hashes. It automatically upgrades versions 1–6 to version 7; older clients reject the newer schema. Version 5 protected immutable compatibility origins. Version 6 adds deduplicated pre-tool Git scopes and a capability floor for ordered checkpoint recovery. Version 7 protects message model/native/path metadata from older writers. Stop agent/collector/watch processes and back up the private ledger before replacing binaries. Install CLI 0.8.0 or newer before opening that ledger again. The server database is on migration 28; upgrade the server before using new collector metadata. At most 64 unfinished tools per worktree are admitted. Terminal IDs remain for exact replay; retention/erasure and automatic installer repair remain separate work. To upgrade the plugin, stop OpenCode, disable the verified owned installation with the new binary, enable it again and restart.

## Adapter protocol

`gitmora capture` reads one strict JSON object from stdin, up to 3 MiB before domain validation. It returns the scoped Gitmora `session_id`. Each observation contains the full completed conversation so far and this turn's observed changed files:

```json
{
  "schema_version": 1,
  "source_session_id": "native-agent-session-123",
  "event_id": "completed-turn-2",
  "agent": {"name": "generic-local", "version": "1", "model": "local-model"},
  "started_at": "2026-10-30T10:00:00Z",
  "messages": [
    {"role": "user", "text": "Preserve failed deliveries"},
    {"role": "assistant", "text": "The queue now retains failed uploads"},
    {"role": "tool", "tool_name": "shell", "text": "Relevant checks passed"}
  ],
  "changed_files": ["src/queue.go"]
}
```

`started_at`, `ended_at` and agent `version` are optional. For completed sessions provide their actual start/end timestamps. Missing start time uses first observation time. Source/event IDs are nonempty bounded UTF-8 strings; persistent mapping/event keys use digests instead of arbitrary raw identifiers. Gitmora session mapping includes repository, canonical worktree identity, integration name, native source ID and any immutable legacy source scope.

The event ID must remain stable on retry. Repeating the same event/content is acknowledged locally; changed content under the same event ID conflicts. In the generic protocol a later observation must preserve the recorded message prefix. Shorter snapshots, historical edits and compacted replacements conflict. Native continuations use the additional `native_history` protocol described in [native windows](native-continuation.md). Submit completed messages rather than partial streaming fragments.

Adapters replaying cumulative native file history can provide `file_events: [{"id":"immutable-tool-or-patch-ID","path":"src/queue.go"}]` instead of repeating `changed_files`. The ledger remembers each event/path identity across process restart. `changed_files` remains a per-observation delta. Neither field represents line-level authorship.

`capture --pin-source` pins the native source's original identity. The compatibility option `--observe-head-from <full-SHA|unborn>` links the current Git HEAD window and requires source pinning; it cannot recover an unobserved end after a crash. The shipped native plugin uses the durable journal below instead. `--sync` attempts eligible delivery for at most two seconds after durable recording; delivery failure keeps a successful recording response and leaves the queue for retry.

The shared capture contract bounds messages, files and canonical payload to 2 MiB. Known secret patterns are redacted before local persistence. Original agent-owned files/logs are outside the collector's storage boundary. Message order is preserved. Changed paths are canonical relative paths and are accumulated separately as pending commit evidence.

## Commit and rewrite semantics

`post-commit` observes actual changed paths and remaining worktree/index changes. Only recordings in that worktree with overlapping pending observed paths are associated. Completed paths leave the pending set; partially committed files remain available for a later commit. This associates session context and does not establish line authorship.

`post-rewrite` retains original SHAs and adds explicit Git-provided replacement SHAs. Both amend and rebase are tested. No user branch commits or commit messages are added by the collector.

`pre-push` consumes all provided ref updates and selects reachable commits, including intermediate commits. When a remote object is absent locally, selection uses reachable local history. It does not fetch a remote as a side effect. Only the configured named remote and unchanged URL enable automatic context delivery; pushes to other remotes do not retarget context.

Native tool checkpoints freeze the unique commit tips reachable before a tool starts. A checkout of an existing branch therefore creates no new commit interval; commits made after that checkout are retained. This records Git context, not authorship: simultaneous work or a fetch during the tool can introduce commits from another actor. Queries accept at most 4,096 unique pre-tool tips, 2,000 new commits per tool and 200,000 selected commits per push; each Git stdout stream is capped at 16 MiB. Exceeding a bound rejects that association/publication attempt and leaves local data available for explicit recovery. A hook still preserves the existing user hook's exit status.

When any revision of a recording belongs to pushed history, queued historical revisions of that same recording become eligible. This prevents orphaned queue entries after amend/rebase while preserving original observations. Uploaded SHAs remain unconfirmed at the API until the provider connector verifies them.

Pre-push publication intent is retained for the selected commit SHAs even when native association arrives later. It authorizes context delivery, not a claim that Git transport succeeded: a remote can still reject the push. An authorized recording's future complete revisions and native stream continuations inherit eligibility; another source does not. SQLite schema version 7 preserves prior eligible/delivered sessions, native pins and private recordings.

## Offline delivery and recovery

Current state and the immutable delivery queue live at `<Git common directory>/workgraph/state.sqlite`. They are shared across linked worktrees, while observation identities stay separate. The directory and database are private to the current OS user. SQLite WAL, full synchronous commits and immediate write transactions coordinate independent processes.

Network work holds no recording transaction. Exact repository/session/revision/digest acknowledgements remove only their own queued revision. Redirects are refused. Temporary errors retain retry state with bounded backoff; permanent identity/contract conflicts become blocked. Normal collection continues without the server.

```sh
gitmora checkpoint
gitmora sync
gitmora sync --retry
gitmora sync --watch
gitmora sync --include-uncommitted
```

`checkpoint` seals changed current-worktree observations without automatically authorizing uncommitted delivery. Normal `sync` sends only already eligible records. `--retry` explicitly releases pending/blocked failures for immediate retry. `--watch` runs bounded batches every 15 seconds until interrupted. `--include-uncommitted` explicitly selects only the current worktree's uncommitted context; it does not make unrelated worktrees eligible.

Collector work inside a Git hook has a two-second context deadline. Network/capture errors report a bounded diagnostic and retain data; the existing user hook's exit status remains authoritative. An active native OS-store credential works in a GUI-launched Git process without a bearer environment variable. A missing/locked/unavailable credential leaves data local; manual sync or a correctly configured watch process can deliver it later.

## Hook ownership and disable

Gitmora writes its own wrappers in a private Git-directory folder and stores a private installation manifest. Existing hook files remain in place and are called from their original location. Relative hook directories resolve against Git's actual hook working directory. Standard non-collector hooks forward directly through shell wrappers; they do not start an extra Go collector process.

```sh
gitmora disable
```

Disable first persists repository-wide consent, then restores the original repository/worktree `core.hooksPath` setting and removes verified owned wrappers. It stops new capture and future delivery batches; an already in-flight request cannot be recalled. Local capture/queue data remains. A separate private marker keeps consent disabled even if SQLite is damaged or an older enabled database is restored. A changed hook setting or modified wrapper is reported as a conflict and preserved; collection stays disabled. Disable does not require a functioning collector database.

```sh
gitmora enable
gitmora agent disable opencode --local
gitmora agent enable opencode --local
gitmora status
```

`enable` explicitly restores repository collection and owned Git hooks. Named agent controls are independent and remain disabled until explicitly enabled. These settings apply to the shared repository ledger across linked worktrees. Disabled agents neither record nor deliver their queued records; other agents continue delivering without queue starvation. The OpenCode plugin checks this consent before acquiring source history. Removing the global plugin is still a separate `agent disable opencode` command. `status` exposes `collection_enabled` and `disabled_agents`. Updating/reinitializing the same destination does not silently clear named agent consent.

Installation uses an OS file lock and writes/syncs wrappers and metadata before selecting the path. An interrupted first install can leave unselected private installation files; automatic repair of that partial initialization remains part of the operational recovery work. Do not remove user hook files while repairing an installation.

## Verification scope

Tests cover real Git worktrees and bare remotes, multi-commit push, rejecting and accepted existing hooks, exact stdin forwarding, amend/rebase, paths containing spaces/quotes, simultaneous capture processes, SQLite restart/ACK/backoff, HTTP redirects and acknowledgement mismatch. `make smoke` additionally runs built CLI + Git + API + PostgreSQL through API shutdown, offline push, API restart and delivery recovery.

`make verify-linux` compiles CGO-disabled artifacts for the Docker host's Linux architecture and runs collector/SQLite/Git tests as an unprivileged user inside the dedicated test container. It installs Git only into that disposable test container when absent. macOS and Linux arm64 execution have been verified; other OS/CPU runtime checks are not implied.

`npm ci` installs the pinned development-only native test toolchain. `make smoke-opencode` checks TypeScript contracts, loads the installed plugin in the real OpenCode binary, executes actual Bash/Git operations and calls its actual compaction API. It verifies typed summary continuation, original task/consent and exact server parent matching. SDK-shaped synthetic history larger than 2 MiB also passes through the shipped normalizer, built CLI, human commit/push and API/PostgreSQL without losing its tail. The deterministic loopback model exercises agent protocol behavior, not model quality or an arbitrary live model provider.

`make smoke-native-history` separately exercises the actual pinned native server/root SDK with 103 synthetic messages exceeding 3 MiB, two API pages and seven windows. Exact full message text/order, explicit publication consent, parent references and unchanged window/revision replay after CLI/API restart are checked. It is included in the full host gate. The same gate now kills real coordinator processes after durable begin/end/target acceptance and verifies frozen-interval recovery without later human commits or broadened consent. Richer attachment/version/scale acceptance remains open.

Typed compatibility origins and historical provenance are described in the [compatibility runbook](compatibility.md). Local Git acquisition, import CLI and capture/read/index/UI interoperability are implemented and exercised by `make smoke-compatibility`. Origin-bearing historical paths never enter the pending edited-file set. The same immutable import plan retains its original task across replay, and existing publication controls remain in force.

## Message metadata

CLI 0.8.0 retains optional `model`, `provider`, `agent_profile`, `native_id`, `turn_id` and successful native `changes` path reports on individual messages. Models are copied only from exposed producer metadata. Current thread configuration remains on the agent object with an explicit `model_basis`; it is not retroactively assigned as an executing model. A later collector may enrich an older record's missing metadata in a new revision, but cannot rewrite known message/model/path facts. Existing canonical revisions remain unchanged. Metadata does not move native window boundaries. A parent already pinned by a continuation child is not rewritten for metadata-only replay; the active window may receive additive enrichment. Sealed older windows can keep unknown models. See [search and provenance](retrieval.md#agent-model-and-file-provenance).
