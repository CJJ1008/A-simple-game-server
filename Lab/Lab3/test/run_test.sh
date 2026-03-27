#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

cleanup() {
    echo "清理端口 9310 9311 9312 9313..."
    for port in 9310 9311 9312 9313; do
        pid=$(lsof -ti :$port 2>/dev/null) || true
        if [ -n "$pid" ]; then
            kill -9 $pid 2>/dev/null || true
            echo "  已清理端口 $port (pid=$pid)"
        fi
    done
}
trap cleanup EXIT

go run ./runner/main.go "$@"
