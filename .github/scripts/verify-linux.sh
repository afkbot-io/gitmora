#!/usr/bin/env bash
# Only synthetic verification state; no production credentials or deployment operations.
set -euo pipefail
cd "$(dirname "$0")/../.."
[[ "$(uname -s)" == Linux ]] || { echo 'This runner requires Linux.' >&2; exit 1; }
for executable in dbus-run-session gnome-keyring-daemon secret-tool dbus-send; do
  command -v "$executable" >/dev/null
done
verification_root=$(mktemp -d "${TMPDIR:-/tmp}/gitmora-ci-store.XXXXXX")
trap 'rm -rf "$verification_root"' EXIT
mkdir -m 700 "$verification_root/config" "$verification_root/data" "$verification_root/runtime"
export XDG_CONFIG_HOME="$verification_root/config"
export XDG_DATA_HOME="$verification_root/data"
export XDG_RUNTIME_DIR="$verification_root/runtime"
cat > "$verification_root/run.sh" <<'VERIFY'
  printf "%s" "gitmora-ci-ephemeral-unlock" | gnome-keyring-daemon --foreground --unlock --components=secrets >"$XDG_RUNTIME_DIR/keyring.log" 2>&1 &
  verification_keyring_pid=$!
  trap 'kill "$verification_keyring_pid" 2>/dev/null || true; wait "$verification_keyring_pid" 2>/dev/null || true' EXIT
  verification_attempt=0
  until dbus-send --session --dest=org.freedesktop.DBus --type=method_call --print-reply /org/freedesktop/DBus org.freedesktop.DBus.NameHasOwner string:org.freedesktop.secrets | grep -q "boolean true"; do
    verification_attempt=$((verification_attempt+1))
    [[ "$verification_attempt" -lt 50 ]] || { echo "Secret Service did not become ready." >&2; exit 1; }
    sleep 0.1
  done
  make verify-release </dev/null
  make smoke-load </dev/null
  test -s build/load/report.json
VERIFY
dbus-run-session -- bash -euo pipefail "$verification_root/run.sh" </dev/null
