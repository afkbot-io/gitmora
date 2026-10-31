# Compatibility evidence and versioned decoders

Core compatibility includes versioned source decoding, hash-checked immutable Git acquisition, complete window preparation, `gitmora import`, durable native-ledger interoperability and authorized API/search/UI evidence. The real Git/CLI/SQLite/API/PostgreSQL restart workflow uses synthetic source content. The [import tests](../scripts/smoke-compatibility.py) exercise actual Git/CLI/SQLite/API/PostgreSQL behavior. Supported source formats below are explicit; this does not certify every upstream producer version.

## Local commands

Initialize the repository with its existing Gitmora catalog UUID and authenticate using the normal CLI flow. Stop older agent/collector/watch processes before upgrading to ledger version 5. The API and worker must both understand import origins before delivery.

```sh
gitmora import git-ai --commit <full-target-SHA>
gitmora import entire --checkpoint <canonical-ID> --commit <full-target-SHA>
gitmora import entire --checkpoint <canonical-ID> --commit <full-target-SHA> --storage branch
```

Entire defaults to modern per-checkpoint refs and imports every indexed compact-v1 session. `--session <original-session-ID>` selects one. The legacy branch layout is selected explicitly. Missing compact transcripts fail; raw agent logs/attachments are not substituted. Imports require locally available objects. They do not fetch a remote, invoke Entire/Git AI or read external transcript URLs.

The JSON receipt contains local `session_ids`, `windows` and `sources` with exact ref/commit snapshots. For recovery when a source ref may move, retain its full object ID before the first run (`git show-ref --verify <source-ref>`) and supply both `--source-ref <ref>` and `--source-object <full-object-ID>`. The same pair can replay the retained immutable snapshot after the live ref changes. Garbage-collected/missing source objects fail explicitly. Changed source snapshots form new imports; do not use a new snapshot as a retry of an interrupted old one.

All selected inputs/windows are validated and normalized before recording starts. Recording is durable per native window; an interrupted valid prefix is completed by replaying the same source/target/options. Its first task binding remains pinned. The command grants no publication consent. A later selected-commit push uses normal publication rules; `--sync` only attempts already eligible delivery for two seconds. The separate existing `sync --include-uncommitted` command explicitly publishes current private context in the worktree, not only this import.

Each acquired commit/tree/blob is hash-checked using the repository's SHA-1/SHA-256 object format. Raw tree traversal proves selected membership and refuses symlinks/gitlinks. Git AI note attachment must be unique and each range must fit the actual target file; NUL-containing targets are unsupported. Entire requires a checkpoint trailer/anchor/link to the selected target. These checks establish object association, not authorship truth. Aggregate object reads are limited to 64 MiB, commands to five seconds and runtime to 90 seconds; Entire additionally limits indexed sessions to 64 and all selected output to 256 windows/50,000 messages. Older Git builds lacking the required no-lazy-fetch option fail closed.

## Inspected contracts

The source copies in the research extraction are pinned to Entire CLI `df82d37e1f26f9dbb5a0fc78a9b45794aa88a52d` and Git AI `8a6184a3a8fc4e997ccfff3b16d79688e4255af4`. These are inspected format contracts; synthetic decoder fixtures are not an executed end-to-end upstream importer.

- [Entire Transcript Format v1](https://github.com/entireio/cli/blob/df82d37e1f26f9dbb5a0fc78a9b45794aa88a52d/transcript/README.md): JSONL, user/assistant lines, exposed text and inlined tool results, additive unknown fields/types and breaking `v`.
- [Checkpoint metadata](https://github.com/entireio/cli/blob/df82d37e1f26f9dbb5a0fc78a9b45794aa88a52d/api/checkpoint/metadata.go): source/session ID, checkpoint creation time, original agent/model and optional compact transcript offset. Missing offset means a legacy delta; a present offset slices the complete compacted transcript by physical source line. Canonical legacy 12-hex IDs and uppercase ULIDs are accepted. Modern per-checkpoint refs use the ID's last two characters as shard; the legacy branch requires a separate acquisition layout.
- [Git AI authorship/3.0.0](https://github.com/git-ai-project/git-ai/blob/8a6184a3a8fc4e997ccfff3b16d79688e4255af4/specs/git_ai_standard_v3.0.0.md): `refs/notes/ai`, attestation section plus JSON metadata, 7/16-hex legacy keys, session/trace composite keys and explicit known-human keys. Metadata lookups must exist; ranges are positive, ordered and nonoverlapping. A note's `base_commit_sha` is retained separately from the annotated target commit.

The decoder reads no URLs, source files or executables. The implemented acquisition boundary supplies verified immutable object/ref/target scope. A local Git experiment showed `git notes --ref=<commit-SHA>` and equivalent environment/configuration forms do not provide immutable-note lookup on the tested Git. The adapter resolves the pinned notes tree and exact annotated-object path directly; it never falls back to the live notes ref after pinning.

## Projection and limits

Complete selected encoded input is bounded to 64 MiB. Entire permits 50,000 physical JSONL lines, two MiB per scanned line and 200,000 selected blocks; malformed/future-version input fails before a recording plan. Exposed text and tool invocation/results are preserved; unavailable results remain explicitly unavailable. Thinking, image and unknown blocks/types are omitted with coverage counts. External Git AI transcript pointers become an availability flag; their URL/credentials and directing-human fields are not imported or fetched. Stats/custom attributes and unreferenced registry records are not claimed as a complete telemetry import.

Known-secret text redaction runs on each complete exposed block before splitting, preventing a known credential from escaping across fragment boundaries. Window sanitization provides a second pass. Valid UTF-8 text is fragmented at rune boundaries into at most 100 KiB messages. A window targets at most 384 KiB estimated message/claim payload and 1,000 items, then passes the actual capture's two MiB validation. At most 50,000 projected items and 256 windows are admitted. No partial plan is returned on a limit/error. Large metadata/path registries can still exceed a snapshot limit and fail explicitly. This is bounded source projection, not an unlimited or lossless attachment/telemetry promise.

Git AI produces `system` messages explicitly marked `kind: provenance_report`, containing source-reported ranges at the fixed target commit. Empty Entire text slices produce `kind: import_notice`, never invented dialogue. Uncovered lines remain unknown. A source-reported author/session is not a verified platform user, signature, human-presence assertion, AI detector, quality score or task acceptance.

## Immutable origin and evidence

Capture snapshots accept optional `origin` for supported Entire/Git AI formats. It retains system/format/producer, source/checkpoint/session IDs, source object/ref snapshot, raw input digest, fixed target/base commit, original source agent, source offset/physical line span, window position, omissions/external-transcript flag and bounded typed line claims. Digests identify provided bytes; the server does not independently attest external authorship or prove the source object from an origin claim alone.

Each claim retains path, inclusive range, original key and explicit `ai`/`known_human` kind, with the corresponding original agent/session/model or source-reported author. Platform account identity is never inferred from those strings. Every origin target must remain among the capture's contextual commits. The origin is part of immutable server session identity; additional ordinary commit context cannot retarget it. Continuation matching also requires the same external source and adjacent import windows.

Prepared observations reuse the native stream's initial task pin, stable event/page IDs, immutable parent references, exact ACK/retry and publication controls. A changed immutable source/version is a new observation identity; the same immutable import plan replays with its original task. Historical imported paths are references and never become pending edited paths for a later human commit. Imports grant no additional publication permission; existing selected-commit publication intent continues to apply normally.

Exact capture APIs retain structured origin/claims. Indexing adds a quoted `import_origin` part with an explicit unverified-attribution title, under the capture's existing permission/source gates. Revocation closes its search/expansion. The transcript view displays origin/coverage, original source IDs and paginated claims, distinguishes source-reported humans from platform accounts and renders source HTML as inert text. The interface remains locally bundled Tailwind.

## Upgrade and remaining work

Local ledger version 5 is a capability floor for origin-bearing recording JSON. It upgrades versions 1–4 without rewriting existing recording/native/frontier bytes; older clients refuse the newer header instead of dropping origin fields. Stop agent/collector/watch processes before replacing binaries. The server's relational schema is unchanged; update API and worker together before sending origin-bearing captures. Older APIs reject the new fields and older readers do not support their canonical payloads. Existing captures without `origin` retain their old canonical bytes and session identity.

Broader producer/source versions, actual producer-process compatibility and attachments are not claimed by these format/real-Git tests. Current acceptance follows [C01–C18](requirements.md).
