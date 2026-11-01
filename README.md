# Gitmora

Self-hosted development context for teams using local AI agents, GitLab, GitHub and Tasktopia.

Gitmora records allowed agent sessions, links them to commits, merge requests and tasks, and makes the original sources searchable through the web application, CLI and MCP. Developers keep using their own Codex/OpenCode and model configuration.

## How it works

1. A developer connects a repository and explicitly enables an agent adapter.
2. The CLI records exposed session data in a local SQLite queue. Ordinary Git hooks associate eligible sessions with exact commits.
3. Push or explicit sync sends queued context to Gitmora. An outage leaves the queue available for retry.
4. The server verifies GitLab/GitHub commit and MR/PR/CI metadata, reads configured Tasktopia context and indexes authorized sources in PostgreSQL.
5. People search the web workspace; external agents retrieve exact, permission-checked context through CLI/MCP.

Capture is opt-in per repository/agent. A transcript does not establish line-level authorship. There is no built-in model, Q&A service, generated summary or autonomous review runner.

## Start here

- **[User guide, in Russian](docs/getting-started.md)** — account, CLI, agent, push, search and MCP.
- **[Self-hosting](docs/deployment.md)** and [account provider](docs/identity-hosting.md).
- **[Self-Managed GitLab](docs/gitlab-self-managed.md)** — custom HTTPS host, port, installation prefix and corporate CA.
- **[GitHub.com / Enterprise](docs/github.md)** — URL lookup, public access or a dedicated read token, PRs and Actions.
- [Tasktopia](docs/tasktopia.md), [CLI account](docs/cli-auth.md), [standalone installation](docs/native-installation.md).
- [Architecture](docs/system-analysis.md), [API](docs/api.md), [development and tests](docs/development.md).

The UI uses React, TypeScript and Tailwind CSS with light/dark themes and Russian/English. Appearance is selected in personal settings. Web and CLI use one account; public registration is configurable and new users receive no automatic project access. See [account administration](docs/accounts.md).

## Deploy on your own server and domain

Use the **[complete self-hosting guide](docs/deployment.md)** for a new Linux server. It covers one-domain app/login, DNS, ACME/HTTPS renewal, private networks, first administrator, registration policy, source connections and publishing the client downloads. `/install.sh` installs the **developer CLI**, not the platform.

1. Point your domain's DNS at the server and prepare Docker Compose, nginx/Certbot and the source-build toolchains.
2. Check out `v0.4.1` and build the app, operator CLI, PostgreSQL and hosted-account images.
3. Run `gitmorad deployment init` once with the selected overlays; copy [`.env.example`](.env.example) to the private installation's `compose.env` and the [runtime example](deploy/gitmora.runtime.env.example) to its `runtime.env`.
4. Set `GITMORA_PUBLIC_HOST` to your domain, select an unused private subnet, and run `scripts/render-nginx.py` for that hostname. Obtain the certificate with the HTTP bootstrap config, then activate the HTTPS config.
5. Initialize/finalize the same-domain account provider, copy its dedicated OIDC client-secret file into the app slot, and start the app.
6. Create your own user through hidden-password `gitmorad users create`, grant operator authority explicitly, then sign in and create your project.
7. Publish the six checked CLI files on your domain, connect your repositories and configure agents with your own `--server` address.

The private `compose.env` is selected automatically on later deployment operations. It controls Compose interpolation; `runtime.env` controls API/worker runtime. Actual secrets are generated into private file slots. **[Every supported env and setting](docs/configuration.md)** is documented with role, default, format and precedence. [Custom-server CLI/MCP](docs/custom-server.md) includes Codex/OpenCode examples, multiple server accounts and diagnostics. [Relocation](docs/relocation.md) distinguishes a fresh installation, moving existing data and changing an existing OIDC domain.

## Build from source

Requirements: the Go toolchain selected by `go.mod`, Node.js 22.22+ and npm. Docker Compose and Python 3.11+ are needed for integration/deployment checks. Git 2.43+ is required for supported collector workflows.

```sh
npm ci
make build
bin/gitmora --help
bin/gitmorad --help
```

Build outputs are `bin/gitmora` and `bin/gitmorad`. Follow the deployment guide to configure HTTPS, the account provider and private network routes before connecting users. Building alone does not create a running installation.

```sh
npm run test:web
npm run test:agents
npm run check:format:web
make vet
make integration
```

[The full verification guide](docs/development.md) covers browser/provider/native/restore gates and their extra prerequisites. Tests use synthetic isolated data; production credentials are not required.

## Compatibility and boundaries

- Local agents: Codex hooks and OpenCode plugin/SDK; [adapter scope](docs/collector.md) and [Codex setup](docs/codex.md).
- CLI: macOS and Linux, with Keychain or an unlocked Secret Service for interactive account storage. Windows is not supported.
- GitHub.com: read-only API synchronization for exact commits, PRs, Actions and source files; [limits and setup](docs/github.md).
- GitLab: API v4 synchronization, optional authenticated webhooks and explicit per-instance network policy. GitLab membership is not automatically copied into Gitmora.
- [Entire/Git AI import](docs/compatibility.md): explicit versioned formats and preserved provenance, not universal producer compatibility.
- Phone checks use browser emulation. SMTP recovery, automatic external ACL synchronization, unattended upgrades and a production capacity guarantee are not included.

Source/server release is 0.4.1; the published standalone client is 0.4.0 (0.2.0 remains compatible). See [changes](CHANGELOG.md). Legacy `workgraph` executable aliases, `WORKGRAPH_*` variables and protocols remain compatible. Dependency and copied-template terms are listed in [third-party notices](THIRD_PARTY_NOTICES.md).

## License

Gitmora's original code is available under the [MIT License](LICENSE). Separately licensed dependencies and adapted templates retain the terms listed in [third-party notices](THIRD_PARTY_NOTICES.md).
