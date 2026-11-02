# Native windows and exact capture continuation

The shipped OpenCode adapter captures exposed source evidence through deterministic bounded windows. It preserves earlier exact snapshots when the native agent compacts, prunes or edits its available history. This complements the generic collector's strict complete-observation prefix contract.

## Source protocol and limits

The adapter uses stable native message/part IDs to generate hashed identities. Windows target 512 KiB, with at most 1,000 exposed messages and 1,000 file events. Acquisition reads 100 rows/page through the supplied SDK, validates exact session/order/cursor scope and reverses older-first page traversal into chronological history. It allows at most 50,000 rows, 500 API pages and 64 MiB serialized raw source data, with a three-second request and thirty-second pass deadline. Raw byte checks follow SDK parsing. Normalization separately permits 200,000 parts, 64 MiB estimated exposed data and 256 windows. Individual text/tool clipping carries an explicit omission marker. Excessive/malformed/incomplete acquisition fails before recording, retaining prior captures; there is no unbounded-history guarantee. Richer attachment/source-format support remains required.

The pinned OpenCode 1.19.5 legacy endpoint uses exact `X-Next-Cursor` data in the next same-session/directory `before` query. The reader validates its canonical oldest-row ID/time anchor, denies loops/overlaps/scope changes, requires successful SDK metadata and never follows Link destinations. An explicitly empty header is invalid; an absent continuation on a valid response terminates the pass. New messages after the first-page boundary are observed in later lifecycle passes. This is a keyset observation, not an atomic native database snapshot.

Only stable completed parts are captured: an unfinished parallel tool/text part blocks later evidence. Completed compaction summaries start an epoch and carry `kind: agent_summary`. Model summaries are source statements, not replacements or independent verification of their inputs. OpenCode source was checked for the explicit summary and pruning markers; see its pinned [compaction implementation](https://github.com/anomalyco/opencode/blob/v1.19.5/packages/opencode/src/session/compaction.ts).

`capture --pin-source` accepts optional `native_history`:

```json
{
  "page_id": "<canonical SHA-256>",
  "epoch": "<canonical SHA-256>",
  "message_ids": ["<one canonical SHA-256 per exposed message>"],
  "pruned": [0],
  "reason": "window"
}
```

This object is nested in the normal observation JSON. `pruned` is optional and contains zero-based tool-message indices. Caller reason is `window` or `compaction`. Repeated message IDs, malformed hashes and non-tool/out-of-range pruning indices are rejected. The generic `capture` operation rejects this metadata unless source pinning is selected. Callers do not supply a parent session or publication permission through this input.

Each window is durably accepted before the next is recorded. Only the final window attempts bounded delivery, avoiding a network timeout for each old window during offline replay. A partially completed pass can restart safely. Patch-only parts retain file events, and tool HEAD windows survive an initially empty exposed history.

Tool boundaries are now persisted before SDK reads. A transient source failure or killed native process leaves a recoverable journal entry. The exact accepted final window is bound in its recording transaction, and association uses only the stored Git interval. Target-bound entries recover without source access. An unobserved end remains null until explicit operator resolution; current HEAD never fills it automatically. See [durable recovery](collector.md#durable-native-tool-recovery) for the persistence contract.

## Local stream identity

One original native conversation pins a stream root and its initial task/worktree/repository identity. Each bounded window has its own Gitmora session and immutable revision history. A changed completed prefix creates a `history_replacement` child. Identical shorter prefixes are treated as stale partial views; they cannot erase later captured messages. Old exact event replays return their original local session identity without resetting the active branch. Reusing a source ID with a different explicit start time conflicts.

Durable file-event consumption spans the source's windows and replacement branches. A replay cannot reintroduce an already consumed path. Pruning preserves previously captured tool bytes using the stable identity; if the source pruned before first capture, the retained text explicitly reports that the output was unavailable.

Publication permission likewise spans only that original source: a push selecting one member authorizes its source windows, including an already dirty final window after a later human commit. Another source/worktree stays private. A failed Git push can still have authorized context delivery; provider confirmation remains independent.

Commit relations copied into a child are inherited conversation context, not new path or authorship claims. A child created before a later human commit may have no own SHA relation. Exact-SHA search still filters the stored revision's own relations; richer stream-wide context expansion remains required agent-context work.

## Server continuation evidence

Each child capture includes:

```json
{
  "root_session_id": "<initial Gitmora session UUID>",
  "previous_session_id": "<exact prior local snapshot's session UUID>",
  "previous_revision": 4,
  "previous_digest": "<exact prior sanitized snapshot SHA-256>",
  "segment": 2,
  "reason": "compaction"
}
```

The `continuation` field is optional in the existing capture schema. Other reasons are `window`, `history_replacement` and `protocol_upgrade`. The child cannot reference itself; sequence and digests are bounded. Continuation identity is fixed across the child's revisions. Earlier captures' canonical representation stays unchanged when optional fields are absent.

`GET /v1/repositories/{repository}/sessions/{session}` includes `parent_reference_verified: true` only when it independently matches the exact parent bytes/revision, original owner, repository, agent name, start time, task, root and adjacent sequence. Missing/mismatched parents do not become verified. Out-of-order uploads are accepted: a parent can arrive later and make a subsequent read verify the edge.

This verifies that edge, not every ancestor, native chronological order, a summary's correctness, code behavior or task completion. The graph describes local observation acceptance order; preserved windows can overlap during replay/replacement. Original captures remain immutable and independently citable. Search titles explicitly identify agent-produced compaction summaries.

## Upgrade and verification

Migrate/update the API before installing a collector protocol that requires it; this pagination extension changes no server/wire schema. Stop OpenCode, disable the verified owned old plugin, enable the new one and restart OpenCode. The installer preserves differing existing files. The new layout adds private `history.ts`; the new binary still recognizes the exact legacy owned file set for safe disable without removing provider configuration. Enable does not overwrite the old layout or customized/unowned helpers. Automatic in-place installer upgrade/recovery remains separate operational work.

SQLite schema version 7 automatically upgrades versions 1–6, retaining private data, source pins and existing publication permission. Existing pinned legacy sources continue through a `protocol_upgrade` edge when first observed through windows. Mapping/call/event keys remain hashes. The private recovery journal additionally stores bounded native source locator values and sanitized operator reasons so a new process can locate its original source.

The compaction gate distinguishes domain/SQLite/SDK fixtures from actual native execution. The actual OpenCode 1.19.5 compaction API, local collector, Git, built API and PostgreSQL are exercised with a deterministic loopback model. Another scenario sends over 2 MiB of SDK-shaped exposed history through the shipped normalizer and built CLI, then ordinary human commit/push, and checks all messages and exact parent references at the API. The model fixture does not establish model quality or compatibility with broader native-agent versions.

`make smoke-native-history` adds an actual native server/root SDK pass with 103 synthetic messages over 3 MiB, two source pages, seven collector windows, exact API text/order/parent verification and unchanged replay after CLI/API restart. It requires explicit uncommitted-publication consent; pagination does not broaden ordinary push consent. The full host gate includes this scenario. The gate additionally kills three real coordinator processes after durable begin/end/target acceptance, then recovers the exact intervals, excluding later human commits and preserving private consent. Richer attachments, broader source versions and scale need separate acceptance.

Version 5 adds the reader/writer capability floor for [compatibility origins](compatibility.md), retaining version-4 frontier state. The existing native conversation protocol remains unchanged. Stop old collector/agent/watch processes before upgrade.

Version 6 stores shared pre-tool Git ref snapshots privately and rejects earlier clients, which could apply known message endpoints in journal-hash order. Checkpoint targets and message endpoints now commit atomically. Stop old agent/collector/watch processes before upgrading, retain a local ledger backup, and upgrade the OpenCode helper by disabling its verified installation and enabling it again with CLI 0.7.0. Existing source revisions and publication consent are preserved.
