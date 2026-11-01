# History workspace

The workspace fills the available screen. The left sidebar follows the selected project: Search, Repositories, Members, Project settings and CLI/MCP. Personal language, theme, password and keys live in account settings. There is no task tracker connection.

On first login, create a project, paste a GitHub/GitLab repository URL, and enter a read token. Public GitHub repositories can explicitly use anonymous access. Gitmora resolves the provider ID and server from the URL; private/custom providers must first have an operator-approved route. After connecting, the CLI setup is preselected for that repository. A failed connection can be retried without creating duplicate catalog records.

Search accepts text, a full commit SHA, or a synced MR/PR URL. The idle screen shows recent accepted sessions and merge/pull requests. Each list has independent loading, refresh and pagination. Repository filters scope both lists and URL resolution. Opening a source updates the URL so browser Back and direct links work.

Session detail shows exact revisions and messages, imported provenance, confirmed commits and verified continuation windows. MR/PR detail shows its source commits, related sessions, current cached head and CI observations. Source text is rendered as text. A changed head is explicit and requires opening the new version.

`POST /v1/context/browse` / MCP `context.browse` support `kind: session|merge_request`, optional repository, limit and typed `after`. Default ordering is stable identity; `order: recent` sorts descending observation time with identity tie-breakers. Copy `next` verbatim, including `observed_at`, for recent pagination. Cursors grant no permissions. Integration and capture reads are separately authorized. Disabled or stale provider connections hide their MR headers.

`make smoke-history` exercises real browser/API/PostgreSQL history and revisions. `make smoke-design` checks the project/settings layout, themes, language and mobile overflow. `make smoke-onboarding` checks signed first-login, repository lookup and scoped team access with controlled provider fixtures. Fixture data is synthetic; successful source reads are not browser mocks.
