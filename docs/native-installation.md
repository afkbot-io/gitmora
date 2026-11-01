# Install the standalone Gitmora client

The current native client includes Codex/OpenCode adapters, native CLI API/MCP and the SQLite collector. Developer installation needs no Go, Node.js, npm or server source checkout. Git 2.43 or newer must already be installed. OpenCode itself remains a separately installed agent. macOS authentication uses Keychain; Linux uses `secret-tool` plus an unlocked Secret Service/D-Bus session. Headless Linux has no plaintext credential fallback. See [native authentication](cli-auth.md).

## One-command installation

```sh
curl -fsS --proto '=https' --tlsv1.2 https://gitmora.tasktopia.online/install.sh | sh -s -- --server https://gitmora.tasktopia.online
export PATH="$HOME/.local/bin:$PATH"
gitmora auth login --server https://gitmora.tasktopia.online
```

The explicit `--server` is required: the installer has no fallback to another Gitmora instance. The script detects macOS/Linux and arm64/amd64, downloads the pinned version plus both manifests, and verifies the exact binary and metadata checksums before execution. It installs only into your user-owned stable directory and does not edit shell profiles or agent configuration. A different existing binary requires the explicit update procedure below. For a custom Gitmora URL pass it to the script with `sh -s -- --server https://gitmora.example.com`.

## Select and verify a release

The selected installation publishes **0.7.1**: [release metadata](https://gitmora.tasktopia.online/downloads/cli/0.7.1/release.json) and [checksums](https://gitmora.tasktopia.online/downloads/cli/0.7.1/SHA256SUMS). Your installation can publish its own reviewed artifacts using the build procedure below. Versioned public artifacts live under `https://gitmora.tasktopia.online/downloads/cli/<release-version>/`. Each directory contains those two manifests and four standalone files:

| Machine             | File                                                                                                        |
| ------------------- | ----------------------------------------------------------------------------------------------------------- |
| macOS Apple Silicon | [gitmora_darwin_arm64](https://gitmora.tasktopia.online/downloads/cli/0.7.1/gitmora_darwin_arm64) |
| macOS Intel         | [gitmora_darwin_amd64](https://gitmora.tasktopia.online/downloads/cli/0.7.1/gitmora_darwin_amd64) |
| Linux arm64         | [gitmora_linux_arm64](https://gitmora.tasktopia.online/downloads/cli/0.7.1/gitmora_linux_arm64)   |
| Linux x86-64        | [gitmora_linux_amd64](https://gitmora.tasktopia.online/downloads/cli/0.7.1/gitmora_linux_amd64)   |

Use a private new download directory. Download the selected executable, `release.json` and `SHA256SUMS` from the same reviewed versioned HTTPS path. Validate their exact named checksums **before** making the client executable or running it. macOS has `shasum -a 256 -c`; Linux normally has `sha256sum -c`. For Apple Silicon:

```sh
gitmora_download_directory=$(mktemp -d)
cd "$gitmora_download_directory"
curl --proto '=https' --tlsv1.2 --fail --show-error --output gitmora_darwin_arm64 \
  https://gitmora.tasktopia.online/downloads/cli/0.7.1/gitmora_darwin_arm64
curl --proto '=https' --tlsv1.2 --fail --show-error --output release.json \
  https://gitmora.tasktopia.online/downloads/cli/0.7.1/release.json
curl --proto '=https' --tlsv1.2 --fail --show-error --output SHA256SUMS \
  https://gitmora.tasktopia.online/downloads/cli/0.7.1/SHA256SUMS
awk '$2 == "gitmora_darwin_arm64" {binary_count++; print} $2 == "release.json" {manifest_count++; print} END {if (binary_count != 1 || manifest_count != 1) exit 1}' SHA256SUMS > selected.sha256
shasum -a 256 -c selected.sha256
chmod 700 gitmora_darwin_arm64
./gitmora_darwin_arm64 version
```

Require two successful named checksums and inspect the returned version, source revision and platform against `release.json`. A checksum from the same HTTPS authority detects corruption but is not an independent publisher signature. Release signing/notarization and Windows support are not claimed. Intel macOS is cross-built and metadata-checked; its runtime acceptance remains pending. Do not bypass an operating-system trust prompt based on an unexplained downloaded file.

## Install and connect

Install at a stable path so existing hooks/plugin configuration keep selecting updates:

```sh
mkdir -p "$HOME/.local/bin"
./gitmora_darwin_arm64 self install --directory "$HOME/.local/bin"
export PATH="$HOME/.local/bin:$PATH"
gitmora version
gitmora auth login --server https://gitmora.tasktopia.online
```

Substitute the selected filename on another platform. The directory must belong to the current user and have no group/other write permissions. The installer reports JSON `{path,sha256,previous_sha256?,changed}`. It copies only the executable and uses an empty private lock file; no credential, repository, agent or shell profile is changed. `export` above affects only the current shell. A development build reports `development` and an empty source revision; do not describe it as a published release.

Login requires an enabled Gitmora account and current project membership. Enabled signup binds the exact verified provider subject automatically; when signup is closed, an operator can create and bind an account through `gitmorad users create` with hidden password input. Compare the terminal/browser confirmation code and approve only your own terminal's requested scopes/lifetime. The independent CLI root is stored in the native OS store; ordinary hooks never launch login. The installation operator account does not automatically grant every developer repository access.

Inside your cloned working tree, use the repository UUID shown by Gitmora’s connection instructions. The UUID below is an example:

```sh
gitmora init --server https://gitmora.tasktopia.online \
  --repository-id 11111111-1111-4111-8111-111111111111
gitmora agent enable opencode
gitmora status
```

Work in the configured agent, then commit and push normally. Explicitly authorized source observations are durably queued, scoped and sent under the current CLI credential. An offline push preserves the queue; `gitmora sync --retry` drains it after connectivity returns. Later unrelated human work is not assigned to an already sealed agent source. GitLab independently confirms SHAs/MRs/CI; successful local push is not provider confirmation. See [collector lifecycle](collector.md), [context workflow](context.md) and [local Codex](codex.md).

## Deliberate updates and removal

Download/check a new release as above. Inspect the installed `gitmora version` and its SHA-256, then authorize exactly those existing bytes:

```sh
./gitmora_darwin_arm64 self install --directory "$HOME/.local/bin" \
  --replace-sha256 <inspected-current-executable-SHA256>
gitmora version
```

Without an exact replacement hash, different existing bytes are preserved. Identical incoming bytes are an idempotent observation, including recovery after a lost successful acknowledgment. The installer stages, hashes, synchronizes and atomically renames an ordinary file at the stable path; no versioned executable symlink is used. Cancellation/pre-rename failure preserves the old file. An uncertain directory-sync result requires rerunning the same client and checking its receipt. A hard kill can leave a private `.gitmora-stage-*` file; it is never selected as the executable and is not automatically removed as an assumed owned file on later runs. This is a local-filesystem operation, not distributed/NFS update coordination.

The installed agent hooks/plugins are owned independently. Codex additionally requires review through `/hooks` after changed definitions. The OpenCode files are hash-owned independently. A later release that changes those adapter files needs deliberate `gitmora agent disable opencode` followed by `enable`, after stopping the agent; install alone does not overwrite its configuration. Old executable processes keep their already-open image until they exit; new hook/CLI invocations use the replaced stable file.

Before removing the binary, use `gitmora disable` in each initialized working tree and `gitmora agent disable opencode`, then explicitly revoke/forget the device through `gitmora auth logout --server ...` if desired. Only then remove the reviewed installed file. Local evidence retention and server credential revocation are separate operations; binary removal alone does neither.

## Build and verification

From clean committed source with the pinned Go toolchain and existing output parent:

```sh
python3 scripts/release-cli.py --version <numeric-release-version> --output /absolute/new-release-directory
```

For a fresh server 0.7.1, the [deployment guide](deployment.md#7-раздать-cli-и-подключить-агенты-к-своему-адресу) publishes the matching official client 0.7.1. If distributing a different client version, align `publicClientVersion` in `web/src/domain/onboarding.ts` and `gitmora_version` in `web/public/install.sh`, rebuild the web/server image, then publish the matching six files using `scripts/publish-cli.py`. Runtime env does not change the pinned download version.

The script refuses dirty source/existing output and exports only the selected committed revision into a new owned build tree; ignored compiler/embed inputs never enter it. It verifies module checksums, builds CGO-disabled baseline-CPU targets, checks embedded module/platform/toolchain settings and emits complete public manifests. Native-host execution also checks the exact version/revision stamp. Go deliberately omits linker flags from build info under `-trimpath`; other-platform runtime stamps need actual execution, not an assumption that these flags are inspectable. No timestamp/host path is included in release metadata. It does not publish or install. `--target linux/amd64` can prepare a deliberate subset; normal distribution uses all four.

`make smoke-cli-release` exercises real standalone install, exact-byte update, unchanged Git hook/OpenCode paths, offline SQLite queue, ordinary push after update and API restart with isolated PostgreSQL. Its two explicitly labelled build fixtures do not establish a published release. `python3 scripts/smoke-cli-release.py --release <directory>` also validates actual release artifacts and executes the native selected one. `make smoke-cli-downloads` exercises the actual nginx download route with synthetic public bytes, write/list/symlink denial and preserved app/identity routing. Run the relevant [development gates](development.md) against the exact bytes before publishing.
