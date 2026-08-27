#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

unformatted=$(find "$ROOT/cmd" "$ROOT/internal" -type f -name '*.go' -exec gofmt -l {} +)
if [ -n "$unformatted" ]; then
    printf 'gofmt is required for:\n%s\n' "$unformatted" >&2
    exit 1
fi

cd "$ROOT"
go vet ./...
go test ./...
