# Core architecture


## Flow and ownership

1. A developer enables capture for an agent/repository and selects the repository.
2. The adapter records only exposed conversation and observed tool/file activity. It does not infer hidden reasoning or claim line-level authorship from a transcript.
3. The local collector sanitizes, checkpoints and queues immutable revisions in SQLite. Ordinary Git hooks link eligible checkpoints to exact commits; existing hooks retain their behavior.
4. Push flushes queued context with bounded network work. An unavailable server leaves a recoverable queue; a successful code push is not evidence of successful context delivery.
5. GitLab API reconciliation verifies commit/MR/CI metadata. Webhooks can accelerate discovery; they never replace verification or periodic reconciliation.
7. A separate ordinary worker indexes captured sessions and verified code. PostgreSQL full-text search applies current project/source access before ranking and limits.
8. People browse exact evidence in the web workspace. External agents use CLI/MCP to discover sources, retrieve bounded packs and expand exact versions. They run their own AI.

| Boundary | Authority |
|---|---|
| Identity / directory | One browser/CLI account, local membership, operators and scoped credential trees |
| Capture / collector | Immutable sanitized sessions, offline events and explicit provenance |
| Forge | Verified GitLab commits, MR heads, CI and code observations |
| Retrieval / context | Derived searchable projections and bounded source packs |
| Distribution / operations | Private deployment, signed-in native clients, exact release bytes and recovery |

Domain packages own invariants without SQL/HTTP/OS dependencies. Application services coordinate narrow ports; adapters own PostgreSQL, SQLite, filesystem, Git and provider protocols. The platform is a modular Go monolith with independently runnable API and sync/index worker, plus a native CLI. React/TypeScript/Tailwind provides the human workspace. Existing same-origin hosted identity handles passwords and browser OIDC; the application owns project authorization.

## Failure and trust

Source text remains inert evidence, including when an agent quotes it. Exact digest/version references survive later edits. Client-claimed SHAs remain unconfirmed until the configured provider verifies them. Captured sessions and imported provenance do not establish exact line authorship.

Mutations use bounded typed input, explicit scopes and current membership, immutable request identities and version checks where applicable. Credentials remain in a system secret store or private operator state; provider tokens are encrypted with a separate recovery key. Disabling registration leaves existing authentication intact and never enables anonymous access. New accounts gain no implicit project/operator role.

Offline retries preserve exact intent; acknowledgements remove only the exact queued revision. Source unavailability, user disablement, token revocation and project loss are rechecked before protected reads. Ordinary workers have no model key or autonomous execution authority. Historical migrations retain their checksums; retired modules are dropped by a new atomic migration.

## Verification

Use domain/application behavior tests, real SQLite/Git/worktrees/bare remotes, PostgreSQL concurrency/access/migration checks, real API and CLI processes, actual external agent protocol paths, signed browser/hosted identity tests and isolated deployment/restore exercises. Protocol fixtures are labelled; they do not prove model quality or production-provider acceptance. See the [supported scope](implementation-status.md) for product boundaries.
