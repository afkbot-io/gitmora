#!/bin/sh
# Gitmora CLI installer. Downloads are verified before any executable is run.
set -eu
gitmora_server=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --server) [ "$#" -ge 2 ] || exit 2; gitmora_server=$2; shift 2 ;;
    *) printf '%s\n' 'Usage: sh install.sh --server https://gitmora.example.com' >&2; exit 2 ;;
  esac
done
[ -n "$gitmora_server" ] || { printf '%s\n' 'Pass --server with your Gitmora HTTPS address.' >&2; exit 2; }
case "$gitmora_server" in https://* ) ;; *) printf '%s\n' 'Use an HTTPS Gitmora address.' >&2; exit 2 ;; esac
case "$gitmora_server" in *\?*|*\#*|*@*|*\'*) printf '%s\n' 'Invalid Gitmora address.' >&2; exit 2 ;; esac
gitmora_server=${gitmora_server%/}
case "$(uname -s)" in Darwin) gitmora_os=darwin ;; Linux) gitmora_os=linux ;; *) printf '%s\n' 'Supported systems: macOS and Linux.' >&2; exit 1 ;; esac
case "$(uname -m)" in arm64|aarch64) gitmora_arch=arm64 ;; x86_64|amd64) gitmora_arch=amd64 ;; *) printf '%s\n' 'Supported architectures: arm64 and amd64.' >&2; exit 1 ;; esac
command -v curl >/dev/null || { printf '%s\n' 'Install curl first.' >&2; exit 1; }
gitmora_file=gitmora_${gitmora_os}_${gitmora_arch}
gitmora_version=0.7.1
gitmora_temp=$(mktemp -d)
trap 'rm -rf "$gitmora_temp"' EXIT HUP INT TERM
(
  cd "$gitmora_temp"
  for gitmora_asset in "$gitmora_file" release.json SHA256SUMS; do
    curl --proto '=https' --tlsv1.2 --fail --silent --show-error --max-time 120 --output "$gitmora_asset" "$gitmora_server/downloads/cli/$gitmora_version/$gitmora_asset"
  done
  awk -v binary="$gitmora_file" '$2==binary {b++;print} $2=="release.json" {m++;print} END {if(b!=1||m!=1)exit 1}' SHA256SUMS > selected.sha256
  if command -v sha256sum >/dev/null; then sha256sum -c selected.sha256; else shasum -a 256 -c selected.sha256; fi
  chmod 700 "$gitmora_file"
  "./$gitmora_file" version
  mkdir -p "$HOME/.local/bin"
  "./$gitmora_file" self install --directory "$HOME/.local/bin"
)
printf '\n%s\n' 'Gitmora CLI installed. Open a new terminal after adding $HOME/.local/bin to PATH.'
printf '%s\n' 'For this terminal: export PATH="$HOME/.local/bin:$PATH"'
