# GitHub.com and GitHub Enterprise

Gitmora can verify exact commits and index their changed source files from GitHub.com, synchronize pull requests and GitHub Actions runs, and connect that evidence to captured agent sessions. GitLab remains supported independently, including Self-Managed instances. Local project membership determines access inside Gitmora.

## Enable on your server

Append `deploy/compose.github-com.yaml` to the installation's existing ordered Compose policies. It enables `WORKGRAPH_GITHUB_ENABLED=true` and gives API/worker a private CONNECT relay whose only destination is `api.github.com:443`. Keep existing GitLab/Tasktopia/identity overlays when adding it. Follow [deployment](deployment.md) for preparing, inspecting and activating the complete profile.

For a native development server, set `WORKGRAPH_GITHUB_ENABLED=true`. The normal API is fixed to `https://api.github.com`; `WORKGRAPH_GITHUB_API_URL` accepts only an explicit loopback fixture override. `WORKGRAPH_GITHUB_PROXY_URL` is an explicit private HTTP CONNECT proxy. Ambient HTTP proxy variables do not select a route. Redirects are rejected, including repository moves; register the new canonical identity rather than silently transferring stored credentials.

## Register and connect

1. Sign in or register and create your project.
2. Open **Repositories**, choose **GitHub**, and enter the full repository URL and a dedicated read token. Both public and private repositories are resolved before registration; the immutable numeric ID is detected automatically.
3. Choose **Connect repository**. Public repositories can explicitly use anonymous access for a small initial test.
4. Use **Project settings → Integrations** to replace the token, inspect synchronization or disconnect. Failed verification never replaces an existing connection.
5. Open **CLI and MCP**, install the client, sign in and copy the repository-specific `gitmora init` command. Enable the chosen local agent and commit/push normally.

### GitHub Enterprise

A repository URL can use an operator-approved Enterprise base, including an installation prefix and nondefault HTTPS port. Set `WORKGRAPH_GITHUB_ALLOWLIST` to the comma-separated canonical web bases; their REST API is fixed to `<base>/api/v3`. The GitHub.com API remains independently fixed to `api.github.com`. An unapproved URL never receives the entered token. URL redirects and alternate provider-returned destinations are rejected.

Native deployments use public IPv4 routing by default. `WORKGRAPH_GITHUB_PRIVATE=true` deliberately selects private destinations for the Enterprise routes only. Configure the internal CA in the selected container trust store; TLS verification is not disabled. An explicit `WORKGRAPH_GITHUB_ENTERPRISE_PROXY_URL` selects a private CONNECT proxy independently of the GitHub.com proxy.

For Docker, copy `deploy/compose.github-enterprise.example.yaml` into the installation's private policy folder, fill `GITMORA_GITHUB_ENTERPRISE_URL`, `GITMORA_GITHUB_ENTERPRISE_AUTHORITY` and visibility in the installation `compose.env`, inspect it, and append it to the existing ordered overlays. Keep the GitHub.com overlay when both are used. Choose `--visibility private` on the Enterprise relay only for an intentionally private service. Each fixed relay accepts only its approved authority; additional hosts need corresponding trusted proxy policy. The normal UI accepts custom URLs but never opens private network access by itself.

## Dedicated read token

In GitHub account settings, create a **fine-grained personal access token** restricted to the selected repository, with **Contents**, **Pull requests** and **Actions** repository permissions set to **Read-only**. Metadata access is included by GitHub. Set an expiry and obtain organization approval if required. Paste it only into the Gitmora connection form. Gitmora verifies the immutable numeric ID and canonical URL, encrypts the credential and clears the form. Replacing it fences outstanding worker results from the previous connection generation.

Public access is an explicit mode; a failed token never causes an anonymous retry. GitHub currently allows 60 unauthenticated REST requests per originating IP per hour, so public mode is suitable for a small initial test. Regular use should have a dedicated credential. Rate limits delay durable work without discarding the queue. See [GitHub's rate-limit rules](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api).

## CLI/API and MCP

The catalog representation uses `provider: "github"`, `provider_url: "https://github.com/owner/repository"` and `provider_repository_id`. GitHub records do not expose fictitious GitLab fields. Legacy GitLab catalog bodies remain compatible.

| Operation | Path/body |
| --- | --- |
| Resolve URL with provider and optional token | `POST /v1/projects/{project}/repositories/lookup` with `{"provider":"github","url":"https://github.com/owner/repository","api_token":"<read token>"}`; omit `api_token` for public mode |
| Register | `POST /v1/projects/{project}/repositories` with a fresh `request_id`, repository UUID `id`, `name` and the three provider fields above |
| Configure public | `PUT /v1/repositories/{id}/forge` with `{"anonymous":true,"webhook_mode":"disabled"}` |
| Configure read token | Same path with `api_token` and `webhook_mode: "disabled"`; omit `anonymous` |
| Health / retry / disconnect | `GET` or `DELETE` the connection path; `POST` its `/sync` subpath |
| Pull request / Actions | `GET /v1/repositories/{id}/forge/merge-requests/{number}` or `/pipelines` |

Use [authenticated JSON CLI operations](native-context.md). Do not place service tokens in shell history. MCP's existing six read-only context tools work with authorized GitHub sources without a separate model or server agent.

## Evidence and limits

The server checks exact returned commit SHAs. A pushed session assertion alone does not confirm a commit. Files are read through immutable Git commit/tree/blob objects, with object-hash verification, size limits and secret redaction. Symlinks, submodules, large/binary files and unsupported paths have explicit skip reasons. Source enumeration inherits GitHub's commit-diff cap; a complete repository mirror is not claimed.

Pull-request detail and each commit page are fenced by the same code identity before and after acquisition. GitHub's [PR commit endpoint](https://docs.github.com/en/rest/pulls/pulls#list-commits-on-a-pull-request) returns at most 250 commits: larger PRs remain incomplete and do not publish completed merge lineage. A synthetic merge SHA on an open PR is never shown as a completed merge. Actions keep their exact tested SHA; a synthetic merge run does not automatically apply to a source head.

Synchronization currently uses periodic API polling and explicit retry. GitHub webhooks, deployments, child pipelines, review comments and autonomous review execution are not included. Only the PR and Actions synchronization streams are advertised for GitHub. Disconnect removes recoverable credentials and hides provider sources while preserving captured user sessions.

`make smoke-github` exercises real Git objects, the installed native client, offline push/retry, PostgreSQL persistence, immutable source acquisition, PR linkage, search and HTTP MCP against an isolated protocol fixture. `make smoke-github-ui` checks signed browser setup, both access modes, RU/EN, desktop and emulated iPhone. These are fixture-backed tests; live upstream acceptance is reported separately.
