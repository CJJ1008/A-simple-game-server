#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
TARGET_DIR="$(cd "$SCRIPT_DIR/../student" && pwd)"
LOG_FILE="${TMPDIR:-/tmp}/lab3_student_server.log"
DATA_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/lab3_test_data.XXXXXX")"

check_ports() {
  local occupied
  occupied="$(lsof -nP -iTCP:9310-9313 -sTCP:LISTEN 2>/dev/null || true)"
  if [[ -n "$occupied" ]]; then
    echo "检测到 9310-9313 端口已被占用，student 服务无法启动。"
    echo "请先关闭残留的 Lab3 服务进程，再重新运行测试。"
    echo
    echo "$occupied"
    exit 1
  fi
}

cleanup() {
  if [[ -n "${SERVER_PID:-}" ]]; then
    kill "$SERVER_PID" >/dev/null 2>&1 || true
  fi
  rm -rf "$DATA_ROOT"
}

trap cleanup EXIT

check_ports

cd "$TARGET_DIR"
mkdir -p .gocache
LAB3_DATA_ROOT="$DATA_ROOT" GOCACHE="$TARGET_DIR/.gocache" go run ./cmd/server >"$LOG_FILE" 2>&1 &
SERVER_PID=$!

sleep 2

if ! kill -0 "$SERVER_PID" >/dev/null 2>&1; then
  echo "student 服务启动失败，服务端日志如下："
  echo
  cat "$LOG_FILE"
  exit 1
fi

cd "$SCRIPT_DIR"
set +e
GOCACHE="$TARGET_DIR/.gocache" go run ./autotest.go "$@"
STATUS=$?
set -e

if [[ $STATUS -ne 0 ]]; then
  echo
  echo "student 服务端日志如下："
  echo
  cat "$LOG_FILE"
fi

exit $STATUS
