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
CGO_ENABLED=0 GOOS=linux GOARCH="$target_arch" go test -c -o bin/verification/identity-keychain.test ./internal/identity/keychain
docker compose -f compose.test.yaml exec -T -u 0 postgres sh -c 'command -v secret-tool >/dev/null && command -v gnome-keyring-daemon >/dev/null && command -v dbus-run-session >/dev/null || apk add --no-cache libsecret dbus gnome-keyring'
container_id=$(docker compose -f compose.test.yaml ps -q postgres)
docker cp bin/verification/identity-keychain.test "$container_id:/tmp/workgraph-native-keychain.test"
docker cp internal/identity/keychain/testdata/run-linux.sh "$container_id:/tmp/workgraph-native-keychain-run.sh"
docker compose -f compose.test.yaml exec -T -u postgres postgres sh /tmp/workgraph-native-keychain-run.sh
