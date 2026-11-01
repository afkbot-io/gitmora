# Supported core scope

The current product is development context, described by [requirements C01–C18](requirements.md) and the [architecture](system-analysis.md). Source/server version is 0.5.0; the standalone client is 0.5.0.

| Area | Delivered behavior |
| --- | --- |
| Collection | Opt-in Codex/OpenCode adapters, sanitized immutable sessions, local SQLite queue and explicit disable/recovery. |
| Git | Ordinary commit/push, existing hooks, linked worktrees and exact source windows; GitLab/GitHub independently verify commit/MR/CI metadata. |
| Search | PostgreSQL lexical retrieval, current authorization before results and exact version/fragment expansion. |
| Agent access | JSON CLI actions and six read-only MCP context tools. AI work uses the external agent's own configuration. |
| Accounts | Shared web/CLI account, cookie/CSRF browser sessions, native OS-store CLI roots and bounded revocable credentials. |
| Registration | Env-controlled signup and hidden-password operator creation; no implicit project/operator authority. |
| Workspace | React/TypeScript/Tailwind, RU/EN, personal appearance settings, full-width project workspace, self-service projects, nickname/UUID membership, simplified connections and one-command CLI installation. |
| Operations | Separate API/worker/gateway and identity/database profiles, private file secrets, pinned builds and checksum-checked migrations; persistent per-installation Compose settings, generated own-domain nginx and explicit custom-provider relays. |

[Development](development.md) lists reproducible unit, integration, browser, native, provider, deployment and restore gates. Test fixtures and actual upstream-service checks are different forms of evidence; a green fixture does not certify every provider version or deployment.

Operators configure service credentials, routes/CA, project membership, backups and developer enrollment for their installation. Physical-phone coverage, SMTP recovery, external ACL synchronization, signing/notarization, unattended updates and a production capacity guarantee are not included. Server inference and autonomous review are outside this scope.
