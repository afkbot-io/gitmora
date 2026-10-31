#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
docker compose -f compose.test.yaml up -d --wait
architecture=$(docker info --format '{{.Architecture}}')
case "$architecture" in
  aarch64|arm64) target_arch=arm64 ;;
  x86_64|amd64) target_arch=amd64 ;;
  *) echo "Unsupported test-container architecture" >&2; exit 1 ;;
esac
mkdir -p bin/verification
CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go build -trimpath -o bin/verification/workgraph ./cmd/workgraph
CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go build -trimpath -o bin/verification/workgraphd ./cmd/workgraphd
CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go test -c -o bin/verification/daemon.test ./cmd/workgraphd
CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go test -c -o bin/verification/collector-cli.test ./cmd/workgraph
CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go test -c -o bin/verification/collector-sqlite.test ./internal/collector/sqlite
CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go test -c -o bin/verification/capture-domain.test ./internal/capture/domain
CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go test -c -o bin/verification/compatibility-domain.test ./internal/compatibility/domain
CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go test -c -o bin/verification/compatibility-gitsource.test ./internal/compatibility/gitsource
CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go test -c -o bin/verification/collector-git.test ./internal/collector/gitrepo
CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go test -c -o bin/verification/git-local-policy.test ./internal/gitlocal
CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go test -c -o bin/verification/agent-install.test ./internal/agent/opencode
CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go test -c -o bin/verification/codex-adapter.test ./internal/agent/codex
CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go test -c -o bin/verification/tasktopia-mcp.test ./internal/work/tasktopia
CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go test -c -o bin/verification/task-domain.test ./internal/work/domain
CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go test -c -o bin/verification/task-application.test ./internal/work/application
CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go test -c -o bin/verification/retrieval-domain.test ./internal/retrieval/domain
CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go test -c -o bin/verification/forge-domain.test ./internal/forge/domain
CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go test -c -o bin/verification/gitlab.test ./internal/forge/gitlab
CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go test -c -o bin/verification/contextpack-domain.test ./internal/contextpack/domain
CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go test -c -o bin/verification/contextpack-application.test ./internal/contextpack/application
CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go test -c -o bin/verification/context-http-mcp.test ./internal/transport/httpapi
CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go test -c -o bin/verification/context-native-mcp.test ./internal/contextpack/native
CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go test -c -o bin/verification/native-api.test ./internal/transport/nativeapi
CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go test -c -o bin/verification/webui-http.test ./internal/transport/webui
for module in domain application oidc native nativehttp keychain; do
  CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go test -c -o "bin/verification/identity-$module.test" "./internal/identity/$module"
done
for module in runtimeconfig deployment netroute egress identityhost; do
  CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go test -c -o "bin/verification/platform-$module.test" "./internal/platform/$module"
done
for module in domain local; do
  CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go test -c -o "bin/verification/distribution-$module.test" "./internal/distribution/$module"
done
for module in domain application; do
  CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go test -c -o "bin/verification/recovery-$module.test" "./internal/recovery/$module"
done
for module in domain application; do
  CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go test -c -o "bin/verification/directory-$module.test" "./internal/directory/$module"
done
docker compose -f compose.test.yaml exec -T -u 0 postgres sh -c 'command -v git >/dev/null && command -v python3 >/dev/null || apk add --no-cache git python3'
container_id=$(docker compose -f compose.test.yaml ps -q postgres)
docker cp bin/verification/. "$container_id:/tmp/workgraph-verification/"
docker compose -f compose.test.yaml exec -T -u 0 postgres mkdir -p /tmp/workgraph-verification/compatibility/testdata
docker cp internal/compatibility/domain/testdata/. "$container_id:/tmp/workgraph-verification/compatibility/testdata/"
docker compose -f compose.test.yaml exec -T postgres git --version
docker compose -f compose.test.yaml exec -T -u postgres postgres /tmp/workgraph-verification/collector-sqlite.test -test.v
docker compose -f compose.test.yaml exec -T -u postgres postgres /tmp/workgraph-verification/capture-domain.test -test.v
docker compose -f compose.test.yaml exec -T -u postgres -w /tmp/workgraph-verification/compatibility postgres /tmp/workgraph-verification/compatibility-domain.test -test.v
docker compose -f compose.test.yaml exec -T -u postgres postgres /tmp/workgraph-verification/compatibility-gitsource.test -test.v
docker compose -f compose.test.yaml exec -T -u postgres postgres /tmp/workgraph-verification/collector-git.test -test.v
docker compose -f compose.test.yaml exec -T -u postgres postgres /tmp/workgraph-verification/git-local-policy.test -test.v
docker compose -f compose.test.yaml exec -T -u postgres postgres /tmp/workgraph-verification/agent-install.test -test.v
docker compose -f compose.test.yaml exec -T -u postgres postgres /tmp/workgraph-verification/codex-adapter.test -test.v
docker compose -f compose.test.yaml exec -T -u postgres postgres /tmp/workgraph-verification/workgraphd serve --help
docker compose -f compose.test.yaml exec -T -u postgres postgres /tmp/workgraph-verification/daemon.test -test.v
docker compose -f compose.test.yaml exec -T -u postgres postgres /tmp/workgraph-verification/tasktopia-mcp.test -test.v
docker compose -f compose.test.yaml exec -T -u postgres postgres /tmp/workgraph-verification/task-domain.test -test.v
docker compose -f compose.test.yaml exec -T -u postgres postgres /tmp/workgraph-verification/task-application.test -test.v
docker compose -f compose.test.yaml exec -T -u postgres postgres /tmp/workgraph-verification/retrieval-domain.test -test.v
docker compose -f compose.test.yaml exec -T -u postgres postgres /tmp/workgraph-verification/forge-domain.test -test.v
docker compose -f compose.test.yaml exec -T -u postgres postgres /tmp/workgraph-verification/gitlab.test -test.v
docker compose -f compose.test.yaml exec -T -u postgres postgres /tmp/workgraph-verification/contextpack-domain.test -test.v
docker compose -f compose.test.yaml exec -T -u postgres postgres /tmp/workgraph-verification/contextpack-application.test -test.v
docker compose -f compose.test.yaml exec -T -u postgres postgres /tmp/workgraph-verification/context-http-mcp.test -test.v
docker compose -f compose.test.yaml exec -T -u postgres postgres /tmp/workgraph-verification/context-native-mcp.test -test.v
docker compose -f compose.test.yaml exec -T -u postgres postgres /tmp/workgraph-verification/native-api.test -test.v
for module in domain application oidc native nativehttp keychain; do
  docker compose -f compose.test.yaml exec -T -u postgres postgres "/tmp/workgraph-verification/identity-$module.test" -test.v
done
for module in runtimeconfig deployment netroute egress identityhost; do
  docker compose -f compose.test.yaml exec -T -u postgres postgres "/tmp/workgraph-verification/platform-$module.test" -test.v
done
for module in domain local; do
  docker compose -f compose.test.yaml exec -T -u postgres postgres "/tmp/workgraph-verification/distribution-$module.test" -test.v
done
for module in domain application; do
  docker compose -f compose.test.yaml exec -T -u postgres postgres "/tmp/workgraph-verification/recovery-$module.test" -test.v
done
for module in domain application; do
  docker compose -f compose.test.yaml exec -T -u postgres postgres "/tmp/workgraph-verification/directory-$module.test" -test.v
done
docker compose -f compose.test.yaml exec -T -u postgres -e WORKGRAPH_TEST_CLI=/tmp/workgraph-verification/workgraph postgres /tmp/workgraph-verification/collector-cli.test -test.v

# No OS MIME database, shell, network or writable root exists in this image.
docker build --network=none --file scripts/containers/web-assets.Dockerfile --tag workgraph-web-assets:test bin/verification
docker run --rm --network=none --read-only --user 65532:65532 --cap-drop=ALL --security-opt=no-new-privileges --memory=128m --pids-limit=32 workgraph-web-assets:test -test.v
