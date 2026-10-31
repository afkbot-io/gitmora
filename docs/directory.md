# Local directory and account administration

The directory API manages users, project/repository metadata, local memberships and API keys. All operations reload the current credential/account state. Projects/repositories keep opaque UUIDs; a repository's Gitmora project, canonical GitLab base and numeric provider project ID cannot be changed through this API. Register a new repository UUID for an intentional new identity. GitLab connections still require separate provider verification and the server allowlist.

The [human workspace](web.md) implements project/repository/account/member administration, immutable OIDC binding controls and personal key issuance/revocation, explicit version-conflict recovery, exact in-memory command retries and destructive confirmation. Installation/operator and actual project roles remain separate. The browser smoke gates cover catalog/provider/binding/credential forms and login recovery against API/PostgreSQL with signed local OIDC and Chromium.

Hosted password users are created through [registration or the operator CLI](accounts.md). The low-level directory-only create API does not create a password/provider login; the web account page directs operators to the complete hosted workflow.

## Authority

| Operation | Required scope/authority |
| --- | --- |
| `GET /v1/me` | Any current enabled Gitmora credential |
| Discover project/repository/membership metadata | `directory:read` plus current membership; an operator with `platform:manage` can discover global metadata |
| Rename/delete project; register/rename/delete repository; change memberships | `directory:read,directory:manage` plus project-admin role, or `directory:read,platform:manage` plus installation-operator record |
| Create projects; manage users/operator grants; read global metadata journal | `directory:read,platform:manage` plus installation-operator record |
| Owned key metadata/issuance/revocation | `credentials:manage`; issuance is additionally bounded by the current parent's scopes/expiry |

An installation operator receives no automatic code/transcript/search/integration-management authority. Those existing operations still require their own token scopes and real project membership/role. Adding membership is an explicit journalled operation. `me.user.operator` describes the account; the token's actual scopes still limit what it can do. Project admins cannot turn a `platform:manage` string into an installation role.

New bootstrap makes its first user an installation operator but preserves the original four-scope token. For an upgraded installation, use the trusted database-owner recovery command, then issue explicit scopes:

```sh
bin/workgraphd migrate
bin/workgraphd grant-operator --user-id <existing-enabled-user-UUID>
bin/workgraphd issue-token --user-id <user-UUID> --scopes directory:read,directory:manage,platform:manage,credentials:manage
```

These commands require the server database environment. The grant command records offline origin, creates no user token and adds no project membership. `issue-token` exposes its new bearer once to the operator. Existing users/project admins/tokens gain no new scopes or roles during migration.

## Catalog and account endpoints

All bodies are strict JSON; duplicate/aliased/unknown fields, malformed cursors and excessive data fail before use. Read lists accept only `after_id` (canonical UUID) and `limit` (default 30, maximum 100); responses contain `items` and optional `next_id`. Filters/authorization run before LIMIT. IDs/cursors are never grants.

| Method/path | Body/meaning |
| --- | --- |
| `GET /v1/me` | Current user/operator flag, token ID/scopes/expiry; no secret/hash |
| `GET /v1/projects` | Authorized project metadata; an operator's empty `role` means no membership |
| `POST /v1/projects` | `request_id,id,name`; the creator becomes the initial project admin |
| `GET /v1/projects/{project}` | Current metadata/version and actual caller membership role |
| `PATCH /v1/projects/{project}` | `request_id,expected_version,name` |
| `DELETE /v1/projects/{project}` | `request_id,expected_version`; delete Gitmora-local project data |
| `GET /v1/projects/{project}/repositories` | Bounded repository catalog |
| `POST /v1/projects/{project}/repositories` | `request_id,id,name,gitlab_url,gitlab_project_id` |
| `GET /v1/repositories/{repository}` | Authorized immutable provider identity and current display metadata |
| `PATCH /v1/projects/{project}/repositories/{repository}` | `request_id,expected_version,name`; provider/project retarget fields are rejected |
| `DELETE /v1/projects/{project}/repositories/{repository}` | `request_id,expected_version`; local repository cascade |
| `GET /v1/projects/{project}/members` | Scoped account display/enabled state, role and membership version |
| `PUT /v1/projects/{project}/members/{user}` | `request_id,expected_version,role`; version 0 only for a new membership; role reader/writer/admin |
| `DELETE /v1/projects/{project}/members/{user}` | `request_id,expected_version` |
| `GET /v1/admin/users` / `GET /v1/admin/users/{user}` | Operator-only metadata, including disabled/deleted accounts |
| `POST /v1/admin/users` | `request_id,id,display_name`; creates an enabled account without membership/operator role/credential |
| `PATCH /v1/admin/users/{user}` | `request_id,expected_version`, optional `display_name`/`enabled`; at least one change |
| `DELETE /v1/admin/users/{user}` | `request_id,expected_version`; logical account deletion |
| `PUT /v1/admin/users/{user}/operator` | `request_id,expected_version,operator` boolean |

Fresh create returns 201 with a content-free receipt; other changes/replays return 200. A receipt contains action, actor/target/project IDs, version, project version when relevant, deletion flag, sequence and time. It contains no display names, source text, endpoint credentials or bearer values. Keep the same actor/request UUID and exact normalized input when retrying; a different command under that ID conflicts. A successful exact replay performs no new mutation even after local membership loss/target deletion; it still needs a current credential and relevant operation scope. Replay does not expose resource content or grant role authority. Self-disable/delete revokes the current credential, so further calls require another authorized identity.

Versions are globally monotonic safe integers. Compare an entity's current `version`; stale writes return 409. Project versions change on catalog/membership changes, not every capture/commit. Fetch a membership's own version before role changes/removal; remove/re-add cannot make an old version valid again. A failed journal write rolls back the entire operation. Known credential assignments in display metadata are redacted before hashing/storage.

Last enabled operator/admin protection returns 409: grant another enabled account first. Disabling removes the user's operator grant and revokes every key, while memberships remain inactive. Re-enabling does not revive keys or operator status. Logical deletion additionally removes memberships, records the retired user UUID and changes its name to `Deleted user`; historical actor IDs remain intact. This does not purge all personal data in source documents/transcripts.

Project/repository DELETE physically cascades local evidence, encrypted connections, jobs and projections. Treat this as a destructive operation when building the UI. It does not delete a GitLab repository/MR or Tasktopia task. In-flight accepted external requests cannot be undone by local deletion. Deleted UUIDs return 410 on re-creation; a deleted project's child repository UUIDs are also retired. Other live projects remain independent. Workers cannot publish late local results against removed leases/sources. Retention/archive policy, comprehensive erasure and backup/restore remain separate platform work.

## Scoped self-delegation

`GET /v1/me/tokens` returns bounded owned key metadata, including `kind` (api/browser), `parent_id`, revocation time and scope/expiry. It never returns a bearer or hash. Create a child:

```http
POST /v1/me/tokens
Authorization: Bearer <current credential with credentials:manage>
Content-Type: application/json

{"request_id":"<new UUID>","label":"Local collector","scopes":["capture:read","capture:write"],"ttl_seconds":3600}
```

Requested scopes must exist on the current parent; the child cannot outlive it. TTL is 60 seconds to 90 days, subject to the parent's remaining lifetime. Maximum chain length is eight credentials including the root; at most 100 live keys per account. Roles/membership remain live checks in content owners; a capability name alone grants no project access.

The first 201 response contains `created:true`, key metadata and a one-time `secret`. Retries return 200 with the original ID/current metadata and `created:false`, without a secret. After an uncertain first response, list/revoke that key and use a new request UUID; no recoverable bearer is stored. The workspace preserves the original command/input for an unchanged retry and explains the metadata-only result. Token request IDs share the per-actor command ledger and cannot be reused for another action/input.

`POST /v1/me/tokens/{token}/revoke` accepts `{ "request_id": "<new UUID>" }` and revokes that owned key plus its full descendant tree. Issuance/revocation serialize to prevent a concurrent child escaping the snapshot. The trusted storage revocation method follows the same rule. Cross-user key revocation through this endpoint is denied; account disable lets an operator invalidate all of that account's keys. Enabling an account never clears `revoked_at`.

## Journal and verification

`GET /v1/directory/events?project_id=<UUID>&after_sequence=<integer>&limit=30` returns content-free receipts with `origin` api/offline/oidc. Project management authority can inspect its current project journal; operators can inspect all events or filter a deleted project's ID. `next_sequence` continues the ordered journal. Gaps are normal. Operator/user IDs are pseudonymous metadata; this is not a tamper-proof global audit service and does not yet cover every provider/content operation.

`make smoke-directory` uses the built API, real PostgreSQL and only public catalog/account APIs after bootstrap. It verifies restart/replay, role separation, immutable provider identity, token hierarchy, disable/re-enable/delete, local evidence deletion/tombstones and journal. PostgreSQL race tests add concurrent request identity/admin removal/delegation-revocation, ABA protection, rollback on journal failure and stale index-worker fences. The default full and Linux gates include the module; actual Tasktopia compatibility runs separately. The [OIDC/subject/session backend](identity.md) and human login/catalog/account/member/binding/key views are implemented. Native CLI/OS-store login and the selected production installation are implemented; external ACL synchronization and broader audit/retention remain outside the current core scope. See [current acceptance](implementation-status.md).
