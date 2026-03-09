#!/usr/bin/env bash
# ═══════════════════════════════════════════════════════════════
#  BattleWorld Lab1 测试脚本
#  用法：bash run_test.sh
#  默认测试 student 目录
# ═══════════════════════════════════════════════════════════════
set -euo pipefail

TARGET=${1:-student}           # student 或 complete
SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
LAB_DIR="$SCRIPT_DIR/.."
SRC_DIR="$LAB_DIR/$TARGET"
PORT=9000
SERVER_PID=""

# ─── 颜色 ────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; NC='\033[0m'; BOLD='\033[1m'

info()  { echo -e "${CYAN}[INFO]${NC} $*"; }
ok()    { echo -e "${GREEN}[PASS]${NC} $*"; }
fail()  { echo -e "${RED}[FAIL]${NC} $*"; }
title() { echo -e "\n${BOLD}${YELLOW}$*${NC}"; }

# ─── 清理 ────────────────────────────────────────────────────────
cleanup() {
  if [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT INT TERM

# ─── 先编译，发现语法错误立即报告 ─────────────────────────────────
title "═══ BattleWorld Lab1 自动测试 ═══"
info "目标目录：$SRC_DIR"
info "编译服务器..."
if ! (cd "$SRC_DIR" && go build ./cmd/server 2>&1); then
  fail "服务器编译失败，请检查代码！"
  exit 1
fi
ok "编译成功"

# ─── 辅助函数：启动服务器、运行单项测试 ─────────────────────────────

start_server() {
  # 等待端口释放
  for i in $(seq 1 10); do
    if ! lsof -i ":$PORT" -t >/dev/null 2>&1; then break; fi
    sleep 0.3
  done

  (cd "$SRC_DIR" && go run ./cmd/server) &
  SERVER_PID=$!

  #等待服务器监听
  for i in $(seq 1 20); do
    if nc -z 127.0.0.1 $PORT 2>/dev/null; then return 0; fi
    sleep 0.3
  done
  fail "服务器未能在 6s 内启动"
  return 1
}

stop_server() {
  if [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
    SERVER_PID=""
  fi
  sleep 0.2
}

run_test() {
  local tid="$1"
  local name="$2"
  echo ""
  title "【Test $tid 】$name"
  start_server
  local result=0
  (cd "$SCRIPT_DIR" && go run autotest.go "$tid") || result=$?
  stop_server
  return $result
}

# ─── 执行测试用例 ────────────────────────────────────────────────

TOTAL_PASS=0
TOTAL_FAIL=0

run_case() {
  local tid="$1"; local name="$2"
  if run_test "$tid" "$name"; then
    TOTAL_PASS=$((TOTAL_PASS + 1))
  else
    TOTAL_FAIL=$((TOTAL_FAIL + 1))
  fi
}

run_case 1 "连接与握手（任务 A：Send/Receive）"
run_case 2 "移动上边界保护（任务 B-1：handleMove）"
run_case 3 "移动方向正确性（任务 B-1：handleMove）"
run_case 4 "超出攻击范围（任务 B-2：handleAttack）"
run_case 5 "攻击距离检测（任务 B-2：handleAttack）"
run_case 6 "药水治疗（handleHeal，已提供参考）"
run_case 7 "断线游戏结束（综合）"

# ─── 汇总 ────────────────────────────────────────────────────────
echo ""
echo -e "${BOLD}═══════════════════════════════════════════════════${NC}"
echo -e "  测试汇总：${GREEN}${TOTAL_PASS} 通过${NC}，${RED}${TOTAL_FAIL} 失败${NC}"
echo -e "${BOLD}═══════════════════════════════════════════════════${NC}"

if [ "$TOTAL_FAIL" -eq 0 ]; then
  echo -e "\n${GREEN}🎉 全部通过！实验 Lab1 完成！${NC}\n"
  exit 0
else
  echo -e "\n${RED}❌ 有测试未通过，请检查对应任务的实现。${NC}\n"
  exit 1
fi
