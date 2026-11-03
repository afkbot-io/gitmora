#!/usr/bin/env bash
# Public-shaped fixture addresses are confined to an otherwise disconnected container namespace.
set -euo pipefail
task_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$task_root"
task_arch="$(docker version --format '{{.Server.Arch}}')"
mkdir -p bin/verification
CGO_ENABLED=0 GOOS=linux GOARCH="$task_arch" go build -trimpath -o bin/verification/egress-workgraphd ./cmd/workgraphd
CGO_ENABLED=0 GOOS=linux GOARCH="$task_arch" go build -trimpath -o bin/verification/egress-fixture ./internal/platform/egress/testdata/fixture.go
docker run --rm --network none --cap-add NET_ADMIN --sysctl net.ipv4.ip_unprivileged_port_start=0 \
  --add-host gitlab.verification.invalid:1.1.1.1 --add-host blocked.verification.invalid:127.0.0.1 \
  --mount "type=bind,source=$task_root/bin/verification,target=/verification,readonly" \
  --entrypoint /bin/sh \
  gitmora-postgres:18.6-vector1 \
  -c 'ip address add 1.1.1.1/32 dev lo; exec gosu postgres /verification/egress-fixture /verification/egress-workgraphd'
