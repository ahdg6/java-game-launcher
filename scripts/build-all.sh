#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
LDFLAGS="-s -w"

mkdir -p "$ROOT/dist"

build() {
    goos=$1
    goarch=$2
    output=$3
    printf 'building %s/%s -> %s\n' "$goos" "$goarch" "$output"
    CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch go build -trimpath -ldflags "$LDFLAGS" -o "$ROOT/dist/$output" "$ROOT"
}

build windows amd64 java-game-launcher-windows-amd64.exe
build windows arm64 java-game-launcher-windows-arm64.exe
build linux amd64 java-game-launcher-linux-amd64
build linux arm64 java-game-launcher-linux-arm64
build darwin amd64 java-game-launcher-macos-amd64
build darwin arm64 java-game-launcher-macos-arm64
