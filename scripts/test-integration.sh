#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
docker image inspect gitmora-postgres:18.6-security1 >/dev/null 2>&1 || docker build --file deploy/postgres/Dockerfile --tag gitmora-postgres:18.6-security1 .
docker compose -f compose.test.yaml up -d --wait
unset WORKGRAPH_TEST_CLI
export WORKGRAPH_TEST_DATABASE_URL='postgres://workgraph_test:workgraph_test_only@127.0.0.1:55439/workgraph_test?sslmode=disable'
# Bound concurrent process-heavy packages; retain the real collector's short Git deadlines.
go test -race -p 4 -count=1 ./...
