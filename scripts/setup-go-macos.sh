#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
mkdir -p "$ROOT/.tools"
[[ "$(uname -m)" == arm64 ]] || { echo 'This bootstrap script is for Apple Silicon. Install official Go 1.27.3 for your host architecture.' >&2; exit 1; }
ARCHIVE="$ROOT/.tools/go1.27.3.darwin-arm64.tar.gz"
curl -fL --retry 3 --connect-timeout 20 --max-time 900 -C - https://go.dev/dl/go1.27.3.darwin-arm64.tar.gz -o "$ARCHIVE"
printf '%s  %s\n' dd3908cf20959d798c1e06ebe5b72213f0505ce01373b40ce3110975a6cd99c2 "$ARCHIVE" | shasum -a 256 -c -
tar -xzf "$ARCHIVE" -C "$ROOT/.tools"
"$ROOT/.tools/go/bin/go" version
