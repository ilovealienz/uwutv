#!/usr/bin/env bash
# Build (and optionally run) uwutv.
#   ./build.sh          build to ./uwutv
#   ./build.sh run      build, then run it
#   ./build.sh clean    remove the binary
set -euo pipefail
cd "$(dirname "$0")"

export CGO_ENABLED=0   # no C compiler needed; static binary

case "${1:-build}" in
  clean)
    rm -f uwutv
    echo "removed ./uwutv"
    ;;
  build|run)
    command -v go >/dev/null || { echo "go not found on PATH" >&2; exit 1; }
    # resolve deps / create go.sum on first build or when go.mod changes
    if [ ! -f go.sum ] || [ go.mod -nt go.sum ]; then
      echo "==> go mod tidy"
      go mod tidy
    fi
    echo "==> building"
    go build -trimpath -ldflags="-s -w" -o uwutv .
    echo "built ./uwutv"
    command -v mpv >/dev/null || echo "warning: mpv not found on PATH — playback will fail" >&2
    [ "${1:-build}" = "run" ] && exec ./uwutv
    ;;
  *)
    echo "usage: $0 [build|run|clean]" >&2
    exit 1
    ;;
esac
