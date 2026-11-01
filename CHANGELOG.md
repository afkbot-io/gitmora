# Changes

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
