# Changes

## Unreleased

- Remove unreachable constructors, handler wrappers and the obsolete direct session-association path. Collector tests exercise the same checkpoint/ref-scope path as native adapters.
- Add Linux/macOS source CI and a complete Linux release/load pipeline with isolated native credentials and pinned verification tools.
- Execute CI from a private script file so subprocess stdin cannot swallow the load command; require its report and cover the sequencing with a regression test.
- Make fresh self-hosting and CLI/MCP enrollment explicit in the README. Require the selected identity hostname and use generic nginx templates; validate documentation heading links.
- Keep unprivileged nginx verification logs and temporary paths inside owned fixture directories, preserve failed-test diagnostics on a clean checkout, and verify real read-only CA binds across Compose versions.
- Return browser-session expiry at its actual PostgreSQL storage precision, keeping initial reads and CLI reauthentication consistent across platforms. No schema change.

## 0.7.2

- Verify GitHub commit existence through the exact Git object endpoint. Local-only or rewritten commits remain unconfirmed without blocking synchronization of other commits and PR context. Authentication errors, malformed responses and mismatched SHAs remain errors.
- Exercise an unpublished local object alongside a pushed review through the actual collector, worker, database, search and MCP fixture. No schema changes.

## 0.7.1

- Normalize public account-theme directories/files to 0755/0644 independently of source checkout permissions. A private archive extracted with umask 077 no longer creates unreadable templates in the nonroot provider.
- Verify an image built from restrictive source permissions as UID 1000 and exercise real registration using that image. No database or local-ledger schema changes.

## 0.7.0

- Bind normalized tool positions atomically for Codex and OpenCode; recover pending checkpoints in source order and reject out-of-order application. Missing tool results remain unresolved and block automatic interval consumption from the same source.
- Snapshot pre-tool local Git ref tips so checkout of existing history does not become a new checkpoint; retain newly created commits after checkout and history rewrite.
- Union each pushed ref's own commit interval so one ref cannot exclude another ref's new context. Bound Git stdout and selected push history; disabled agents cannot apply pending checkpoints.
- Upgrade the private collector ledger to schema 6, deduplicating pre-tool ref snapshots and preventing older clients from applying new checkpoints unsafely. Stop old agents/watch processes and back up the ledger before upgrade; reinstall the OpenCode helper with the new CLI. Server schema remains 26.
- Recover recorded Git object IDs in a bounded batch without traversing all repository history. Unknown/quiet commit attempts form conservative barriers; explicit recovery rebuilds only recovered assertions, keeps old immutable revisions and reports unchanged replay accurately.
- Read legacy OpenCode command/result envelopes in compact and expanded tool views. Remove retired Tasktopia targets from Linux/deployment checks and correct obsolete collector instructions.
- Ship the shared Gitmora favicon with the account theme; verify its actual asset response in registration checks.

## 0.6.1

- Add compact PR dialogue pages: long hidden tool outputs become explicitly marked previews with exact source cursors. Expanding a tool reads its complete original through the same PR, revision and head checks.
- Expose `message_view=compact|full` in `context.pack` for agents. Full Unicode-safe pagination remains available; source text is never silently replaced by a preview.
- Test large tool outputs through PostgreSQL, HTTP MCP and real desktop/mobile browsers. Schema remains 26.

## 0.6.0

- Scope PR/commit dialogue reads to recorded half-open message intervals. Full-session navigation no longer lists every associated PR; PR dialogue links retain their selected PR and breadcrumb. Technical capture pages are consumed internally.
- Add bounded `context.pack` transcript pages with exact source revision, original message index, Unicode fragment cursor, MR head and commit-set scope fencing. Missing legacy ranges never fall back to whole-session text.
- Persist optional commit ranges in immutable capture revisions and schema 26 projections. Native Codex frontiers pin the exact normalized tool endpoint, including multiple commits in one stored window; new windows no longer inherit every prior commit.
- Add explicit `gitmora recover-ranges --session LAST_RECORDING_UUID` recovery from successful recorded Git commit output and locally resolved objects. Recovered intervals are labelled separately and preserve original revisions.
- Use one document scroll owner with a sticky project rail; retain append reading position across desktop/mobile.

Upgrade the server first, then CLI 0.6.0. Back up private state and the database before migration 26. Existing records without ranges remain available through explicit full-history navigation; date-only or assistant-text guesses are not used.

## 0.5.2

- Read agent history as a conversation: safe Markdown, user/agent roles, compact tool groups, supplied command/output details and collapsible long messages. Technical revision/source controls stay under details.
- Append message batches, exact source excerpts and linked-context pages without replacing earlier content. Continue verified conversation windows inline and preserve reading position.
- Group verified related windows into one conversation card, showing the request, agent and date. Search links open the exact message and revision. Add conversation-only and loaded-part text filters.
- Use GIN lexical candidate matching before authorization joins, retaining current membership/provider checks before ranking and LIMIT. No model or schema changes.
- Lazy-load the Markdown reader and include all shipped runtime license notices. Verify desktop/mobile, both themes, Markdown safety, continuation identity, pagination and real PostgreSQL/API/MCP search.

## 0.5.1

- Add continuation pagination to session detail and keep the continuation panel open while switching pages.

- Recover long Codex conversations up to 50,000 items while retaining independent 64 MiB protocol/text budgets and per-item limits.
- Retry a changing metadata snapshot up to three times within the shared read deadline. Manual capture has a 60-second recovery budget; hook execution remains bounded.
- Mask explicit Russian/English and Markdown password declarations before native message fragmentation. Historical canonical Text/Lines policies and stored digests remain unchanged.
- Verify long-history completeness, stable snapshot recovery, count limits, secret masking across fragments and projection idempotence.

No schema changes. Upgrade the developer CLI to recover histories that exceeded the earlier 10,000-item bound.

## 0.5.0

- Remove the task tracker connector, task API/search/MCP tools, task binding command, worker jobs and deployment profiles. Historical capture bytes and old migration checksums remain immutable.
- Replace Work with a full-width search workspace, recent sessions and MR/PR lists. Search accepts synced MR/PR URLs, full commit SHAs and text.
- Add read-only `context.resolve`; MR packs include sessions linked to all verified source commits, with scoped fork and provider checks.
- Simplify first-project and repository setup, retain drafts through lookup/connection retries, open repository-specific CLI instructions, and tighten spacing in both themes.
- Update self-hosting configuration, installation, English/Russian copy and live browser/API/CLI/MCP verification.

Upgrade: back up the database and private state before schema 25. Task connector data and derived task indexes are removed; rolling back requires restoring that backup. Update the standalone CLI to 0.5.0 for the new six-tool MCP interface (`context.resolve` replaces `context.task`). Existing capture history and provider credentials are retained.

## 0.4.4

- Stabilize secret masking for quoted assignments followed by punctuation or adjacent assignments. Sanitized code now passes retention validation without blocking later files in the repository queue; existing stable projections keep their digests.
- Add property fuzzing and a PostgreSQL ingestion/restart/search regression for the affected code shape.
- Publish matching server and standalone CLI 0.4.4 builds.

## 0.4.3

- Browser account-switch tests wait for acknowledged logout and cover a deliberately delayed response; failure diagnostics preserve the original error even outside the workspace.

- Unified source/server and standalone CLI release 0.4.3, with matching checked macOS/Linux downloads and onboarding version.
- Fix GitLab diff/MR/pipeline pagination when the provider expands exact route identities and unchanged defaults into Link parameters; reject altered identities, duplicated keys and unexpected filters.
- Includes the portable self-hosting and own-domain configuration improvements from 0.4.1.

## 0.4.1

- Complete self-hosting guide, visible Compose/runtime env examples and a checked configuration reference.
- Persist private Compose interpolation settings beside the selected installation; custom-domain identity routing works without enabling Tasktopia.
- Generate single-domain nginx/ACME/renewal files for custom hosts and loopback ports; no dependency on Certbot nginx helper files.
- Explicit private CONNECT routing for remote Tasktopia MCP, with normal TLS and exact endpoint authorization.
- Custom-server CLI/MCP instructions, atomic checked CLI-file publication and live TLS/installation configuration checks. The installer requires explicit `--server`, without a hidden public-instance fallback. Published standalone CLI 0.4.0 remains compatible.
- MCP behind a native loopback reverse proxy accepts only a validated configured public authority (plus normal loopback access), retaining Origin/auth checks.
- A prepared encryption keyring no longer forces enabling a forge; unconnected installations start while malformed supplied keys are rejected.
- Restarting the identity provider without a registration env override preserves the existing durable signup policy.

## 0.4.0

- Full-width project workspace, simpler connections, consistent light/dark themes and Russian/English interface copy.
- Self-service projects and exact member invitations by nickname or UUID; global account and identity-binding maintenance stays in operator APIs/CLI.
- Repository URL and read-token onboarding with automatic provider ID lookup. Custom GitLab, GitHub Enterprise and Tasktopia instances use explicit operator network policy.
- Project-scoped upstream identities and provider-instance-scoped commit/PR associations prevent cross-company or cross-server evidence sharing.
- Checked macOS/Linux installer at `/install.sh`; project-specific CLI and MCP setup.
- Schema migration 24 adds invitation handles and scopes repository uniqueness to each project.
- Search ranks narrow authorized candidates and fetches full text only for selected results, avoiding unnecessary temporary-file writes. The load gate records an explicit database pool size.

## 0.3.0

- Standalone CLI 0.3.0 with corrected Gitmora command hints and four platform builds.
- Faster lexical ranking by evaluating each distinct language query once, with existing source authorization preserved.

- GitHub.com repository registration by public URL, explicit provider identity and read-only public/token connections.
- Exact commit confirmation, hash-checked source files, guarded PR commit membership and Actions synchronization.
- Provider-neutral connection API and workspace copy; existing GitLab paths and stored identities remain compatible.
- Git-backed offline push/restart/search/MCP and signed desktop/mobile GitHub setup checks.
- Bounded isolated load harness and clearer GitHub/install instructions.
- Existing standalone CLI 0.2.0 remains supported; GitHub webhooks and Enterprise hosts are outside this release.

## 0.2.1

- A compact public presentation and centered account forms.
- Ordinary account sign-in/signup; browser personal-key entry removed. API/CLI/MCP credentials retain their purpose.
- Appearance only in personal settings, shared spacing and shorter interface copy.
- Russian/English selection persists through hosted account entry and back.
- Registration can be disabled through env; hidden-password operator user creation remains available.
- Existing standalone CLI 0.2.0 remains supported.

## 0.2.0

- Consented local Codex/OpenCode capture, offline delivery and ordinary Git workflows.
- GitLab commit/MR/CI and Tasktopia task links, exact source search and six read-only MCP tools.
- Unified account, native CLI enrollment, scoped credentials and project authorization.
- Tailwind light/dark workspace, connection instructions and Self-Managed GitLab profiles.

Server models, built-in Q&A, generated summaries and autonomous MR review are outside this core product.
