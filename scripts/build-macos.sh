#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
ARCH="${ARCH:-arm64}"
case "$ARCH" in arm64) GOARCH_TARGET=arm64;; x86_64) GOARCH_TARGET=amd64;; *) echo 'ARCH must be arm64 or x86_64' >&2; exit 1;; esac
GO="${GO:-$ROOT/.tools/go/bin/go}"
if [[ ! -x "$GO" ]]; then GO="$(command -v go || true)"; fi
if [[ -z "$GO" || ! -x "$GO" ]]; then echo 'Go not found. Run scripts/setup-go-macos.sh first.' >&2; exit 1; fi
if [[ "$($GO version)" != 'go version go1.27.3 '* ]]; then echo 'This build requires the pinned Go 1.27.3.' >&2; exit 1; fi
export GOTOOLCHAIN=local
export GOPATH="${GOPATH:-$ROOT/.cache/gopath}"
export GOCACHE="${GOCACHE:-$ROOT/.cache/go-build}"
SDK="${SDK:-$(xcrun --sdk macosx --show-sdk-path)}"
APP="$ROOT/dist/macos-0.4.1-$ARCH/Fuse.app"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Helpers" "$APP/Contents/Resources" .build/swift-cache
CGO_ENABLED=0 GOOS=darwin GOARCH="$GOARCH_TARGET" "$GO" build -mod=readonly -trimpath -buildvcs=false -ldflags='-s -w' -o "$APP/Contents/Helpers/messenger" ./cmd/client
swiftc -swift-version 5 -O -target "$ARCH-apple-macosx14.0" -sdk "$SDK" -module-cache-path .build/swift-cache -parse-as-library macos/Sources/*.swift -o "$APP/Contents/MacOS/P2PMessenger" -framework SwiftUI -framework AppKit
swiftc -swift-version 5 -sdk "$SDK" -module-cache-path .build/swift-cache macos/Resources/MakeIcon.swift -o .build/make-icon -framework AppKit
.build/make-icon "$APP/Contents/Resources/AppIcon.icns"
cp macos/Resources/Info.plist "$APP/Contents/Info.plist"
cp macos/Resources/Инструкция.html "$APP/Contents/Resources/Инструкция.html"
IDENTITY="${SIGN_IDENTITY:--}"
codesign --force --sign "$IDENTITY" --options runtime --timestamp=none "$APP/Contents/Helpers/messenger"
codesign --force --sign "$IDENTITY" --options runtime --timestamp=none "$APP"
codesign --verify --deep --strict "$APP"
printf 'Built: %s\n' "$APP"
