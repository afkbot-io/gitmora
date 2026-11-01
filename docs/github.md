# GitHub.com

Gitmora can verify exact commits and index their changed source files from GitHub.com, synchronize pull requests and GitHub Actions runs, and connect that evidence to captured agent sessions. GitLab remains supported independently, including Self-Managed instances. Local project membership determines access inside Gitmora.

## Enable on your server

Append `deploy/compose.github-com.yaml` to the installation's existing ordered Compose policies. It enables `WORKGRAPH_GITHUB_ENABLED=true` and gives API/worker a private CONNECT relay whose only destination is `api.github.com:443`. Keep existing GitLab/Tasktopia/identity overlays when adding it. Follow [deployment](deployment.md) for preparing, inspecting and activating the complete profile.

For a native development server, set `WORKGRAPH_GITHUB_ENABLED=true`. The normal API is fixed to `https://api.github.com`; `WORKGRAPH_GITHUB_API_URL` accepts only an explicit loopback fixture override. `WORKGRAPH_GITHUB_PROXY_URL` is an explicit private HTTP CONNECT proxy. Ambient HTTP proxy variables do not select a route. Redirects are rejected, including repository moves; register the new canonical identity rather than silently transferring stored credentials.

## Register and connect

1. Sign in to Gitmora, select your project and open **Repositories → Add repository**.
2. Choose **GitHub** and enter the public repository URL, such as `https://github.com/your-team/your-project`. Gitmora resolves the numeric repository ID and proposes a name. Only an authorized project administrator/operator can register a repository.
3. In **Project settings**, select that repository and choose **Configure GitHub**.
4. Leave **Read the public repository without a token** enabled for an initial public test, or disable it and enter a dedicated token. Choose **Verify and save GitHub**.
5. Open **Connect**, sign in through the standalone CLI and copy the generated `gitmora init` command. Enable the Codex/OpenCode adapter explicitly. Commit and push normally, then inspect the session, confirmed commit and source search.

Browser registration currently resolves public GitHub.com URLs. For a private repository, an administrator can register its exact numeric ID through the catalog API, then configure the read token in project settings. GitHub Enterprise hosts are not supported by this adapter; custom GitLab hosts use [their own profile](gitlab-self-managed.md).

## Dedicated read token

In GitHub account settings, create a **fine-grained personal access token** restricted to the selected repository, with **Contents**, **Pull requests** and **Actions** repository permissions set to **Read-only**. Metadata access is included by GitHub. Set an expiry and obtain organization approval if required. Paste it only into the Gitmora connection form. Gitmora verifies the immutable numeric ID and canonical URL, encrypts the credential and clears the form. Replacing it fences outstanding worker results from the previous connection generation.

Public access is an explicit mode; a failed token never causes an anonymous retry. GitHub currently allows 60 unauthenticated REST requests per originating IP per hour, so public mode is suitable for a small initial test. Regular use should have a dedicated credential. Rate limits delay durable work without discarding the queue. See [GitHub's rate-limit rules](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api).

## CLI/API and MCP

The catalog representation uses `provider: "github"`, `provider_url: "https://github.com/owner/repository"` and `provider_repository_id`. GitHub records do not expose fictitious GitLab fields. Legacy GitLab catalog bodies remain compatible.

| Operation | Path/body |
| --- | --- |
| Resolve public URL | `POST /v1/projects/{project}/repositories/lookup` with `{"url":"https://github.com/owner/repository"}` |
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
