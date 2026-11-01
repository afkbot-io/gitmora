# Self-host Gitmora

Gitmora runs an API, a synchronization/index worker, a credential-free gateway and PostgreSQL. Hosted account entry uses a separate optimized Keycloak/PostgreSQL installation. HTTPS can serve the app and `/auth/` under one domain.

## Build and initialize

Use the toolchains in [development](development.md). Docker Compose must be available to the trusted host operator. Build images on the destination architecture, or explicitly cross-build/transfer reviewed images and the matching operator binary. Do not reuse a running release tag.

```sh
npm ci
make build
make build-image
make build-identity-image
```

`make build-image` also prepares `gitmora-postgres:18.6-security1`. Builds use pinned base digests and lockfiles. The runtime needs no npm, CDN or styling service.

Choose an existing private parent directory and a new installation directory:

```sh
bin/gitmorad deployment init \
  --directory /absolute/private/gitmora-state \
  --compose /absolute/checkout/compose.yaml \
  --image gitmora:local \
  --port 8080
```

`init` refuses existing state. It creates independent database credentials, an encryption keyring and file-secret slots. Keep the directory private and retain its recovery material separately from the database. Edit `runtime.env` using the [non-secret example](../deploy/gitmora.runtime.env.example); preserve its `0600` mode. Set the real public HTTPS origin. Registration defaults to disabled.

```sh
bin/gitmorad deployment up --directory /absolute/private/gitmora-state
bin/gitmorad deployment status --directory /absolute/private/gitmora-state
```

`up` selects existing images with `--no-build --pull never`, waits for migration/API health and preserves the named database volume. `down` stops this installation; it is not an uninstall or volume-removal command.

## Network and provider routes

The gateway publishes only numeric loopback HTTP, normally `127.0.0.1:8080`. Host nginx terminates trusted HTTPS. API/worker/database use an internal Docker network; they have no general Internet route. Only the credential-free gateway joins ingress. No application container receives the host Docker socket.

Before configuring an external provider, provision an explicit reviewed route/DNS/CA policy. Do not attach secret-bearing API/worker services to a general outbound network as a connectivity fix.

- GitHub.com: append [the fixed API relay overlay](../deploy/compose.github-com.yaml); see [repository setup](github.md).
- GitLab.com: use [the exact-authority relay overlay](../deploy/compose.gitlab-com.yaml).
- Self-Managed GitLab: follow [custom host/port/prefix/CA instructions](gitlab-self-managed.md).
- Same-host Tasktopia: [the example overlay](../deploy/compose.tasktopia.yaml) maps its HTTPS hostname to an explicit private host gateway. Adapt the hostname/subnet to the installation and inspect conflicts first.
- Same-host identity: adapt [the API-only host mapping](../deploy/compose.identity.yaml) and [OIDC activation overlay](../deploy/compose.identity-auth.yaml) to the actual HTTPS origin. The worker does not activate identity policy.

For a new installation, repeat `--overlay /absolute/reviewed/overlay.yaml` during `deployment init` in the intended order. Those paths are persisted. Shell substitutions used by Compose must be supplied on every operation; `runtime.env` does not provide Compose interpolation. Do not run `init` again on an existing installation to change its profile.

The sample profiles/nginx files name `gitmora.tasktopia.online` and `tasktopia.online` as concrete examples. Replace the relevant hostname, trusted certificate paths, allowlist and private mapping together for your own installation. A browser repository form cannot grant a new network destination.

## First operator and account entry

Create the first local operator once, attaching to the existing API. Redirect first-only credential output directly into a new private host file; do not use a one-off Compose container that logs it:

```sh
umask 077
set -C
bin/gitmorad deployment admin --directory /absolute/private/gitmora-state -- \
  init-admin --name 'Gitmora administrator' --ttl 168h \
  > /absolute/private/initial-operator.json
```

The bounded API credential is for trusted bootstrap administration, not browser entry. Reinitialization refuses occupied state. Operator metadata authority does not bypass project content membership.

Prepare/finalize the [account provider](identity-hosting.md), configure the RP's exact issuer/client/file secret and bind the provider's exact owner subject to the initial local operator. Keep administrative credentials out of browser storage, logs and source control. Web entry then uses ordinary account forms and protected cookies; CLI uses `gitmora auth login` and explicit consent. [Registration/user administration](accounts.md) covers enabling signup and creating users with a hidden password. Assign project membership separately.

## HTTPS

Adapt [the HTTPS nginx example](../deploy/nginx/gitmora.https.conf) and optional [HTTP ACME bootstrap](../deploy/nginx/gitmora.http.conf). Validate with `nginx -t` before reload. The public proxy exposes only the selected realm/resources under `/auth/`; master/admin/management routes stay private. [The renewal-hook example](../deploy/nginx/20-gitmora-reload) also needs the selected certificate name.

Access logs omit query strings, cookies, authorization headers and bodies; authentication paths disable detailed logs. `/health/ready` checks compatibility/readiness; `/health/live` checks liveness. A running worker/relay is not proof of successful provider authentication or completed indexing.

The download route serves reviewed immutable CLI artifacts from a public directory separate from private state. Build/version/checksum them using [the release procedure](native-installation.md#build-and-verification), then publish only the four executables and their two manifests. The UI's connection guide uses `/downloads/cli/0.2.0/`; provide that supported release before offering its installer. Never copy state, keys or backups into the public root.

## Isolation, update and recovery

Application containers run as UID/GID 65532 with a read-only root, dropped capabilities and bounded temporary storage/resources/logs. The application database role has no superuser/create-role/create-database/replication privilege. Identity has separate state/database and private administration; back up both installations together.

Serialize updates. Preserve exact images, profiles, encryption/identity recovery material and a quiescent database recovery point. Restore into an isolated new target and actually verify records/configuration/identity before activating it. Keep ordinary workers and public ingress stopped while checking a restored database. An image rollback is not a safe rollback of an incompatible migration. Do not edit existing migration checksums or prune shared-server volumes.

`gitmorad recovery metadata`, `prepare` and `quarantine` are low-level trusted-operator primitives. They do not select/restore a target, stop existing processes or provide an end-to-end production restore coordinator. Restoring old data can revive revoked authority; quarantine and verify it before activation. Scheduled encrypted off-host backup, PITR and failure-domain recovery remain the operator's responsibility.

## Verify a new installation

`make smoke-deployment`, `make smoke-recovery`, `make smoke-egress` and the provider/native/browser gates in [development](development.md) own synthetic isolated resources. They are not commands to run against production state. Validate the real installation's HTTPS, signup policy, denied access, CLI enrollment, worker streams and recovery procedure separately.
