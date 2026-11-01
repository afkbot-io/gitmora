# Changes

## 0.4.2

- Unified source/server and standalone CLI release 0.4.2, with matching checked macOS/Linux downloads and onboarding version.
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
