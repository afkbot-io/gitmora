#!/bin/sh
# The dedicated test container's unprivileged user gets an ephemeral D-Bus/native encrypted collection.
set -eu
verification_root=$(mktemp -d /tmp/workgraph-native-store.XXXXXX)
trap 'rm -rf "$verification_root"' EXIT
mkdir -m 700 "$verification_root/config" "$verification_root/data" "$verification_root/runtime"
export XDG_CONFIG_HOME="$verification_root/config"
export XDG_DATA_HOME="$verification_root/data"
export XDG_RUNTIME_DIR="$verification_root/runtime"
dbus-run-session -- sh -eu -c '
  printf "%s" "native-verification-unlock-only" | gnome-keyring-daemon --foreground --unlock --components=secrets >/dev/null 2>&1 &
  verification_keyring_pid=$!
  trap '\''kill "$verification_keyring_pid" 2>/dev/null || true; wait "$verification_keyring_pid" 2>/dev/null || true'\'' EXIT
  verification_attempt=0
  while ! dbus-send --session --dest=org.freedesktop.DBus --type=method_call --print-reply /org/freedesktop/DBus org.freedesktop.DBus.NameHasOwner string:org.freedesktop.secrets | grep -q "boolean true"; do
    verification_attempt=$((verification_attempt+1))
    if [ "$verification_attempt" -ge 30 ]; then echo "Native Secret Service did not become ready" >&2; exit 1; fi
    sleep 0.1
  done
  WORKGRAPH_TEST_NATIVE_KEYCHAIN=1 /tmp/workgraph-native-keychain.test -test.v -test.run=TestActualNativeSecretStoreRoundTrip
'
