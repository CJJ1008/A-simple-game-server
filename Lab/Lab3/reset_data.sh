#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")" && pwd)"

reset_one() {
  local base="$1"
  mkdir -p "$base/data/cold" "$base/data/hot/checkpoints"
  printf '{}\n' > "$base/data/cold/users.json"
  printf '{}\n' > "$base/data/hot/sessions.json"
  find "$base/data/hot/checkpoints" -type f -name '*.json' -delete
}

reset_one "$ROOT_DIR/complete"
reset_one "$ROOT_DIR/student"

printf 'Lab3 数据已清空：complete 与 student 的账号、会话、检查点都已重置。\n'
