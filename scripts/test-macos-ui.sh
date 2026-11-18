#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
mkdir -p .build/swift-cache
SDK="${SDK:-$(xcrun --sdk macosx --show-sdk-path)}"
swiftc -swift-version 5 -target arm64-apple-macosx14.0 -sdk "$SDK" -module-cache-path .build/swift-cache -parse-as-library macos/Sources/Models.swift macos/Sources/CoreBridge.swift macos/Sources/AppModel.swift macos/Sources/FileQueue.swift macos/Tests/BridgeTests.swift -o .build/bridge-tests -framework SwiftUI -framework AppKit
.build/bridge-tests "$ROOT"
