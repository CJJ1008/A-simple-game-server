#!/usr/bin/env bash
# ═══════════════════════════════════════════════════════════════════
#  BattleWorld Lab2 测试脚本
#
#  用法：
#    bash run_test.sh            # 测试 student 目录
#
#  架构说明：
#    Lab2 服务器支持多人持续运行，无需每次测试重启。
#    所有 7 个测试用例共用同一个服务器进程：
#      1. 脚本开始时启动服务器（使用 -race 检测数据竞争）
#      2. 逐个运行 autotest.go 1 ~ 7
#      3. 全部结束后停止服务器
#    这彻底避免了旧版"端口 TIME_WAIT 导致 bind 失败"的问题。
# ═══════════════════════════════════════════════════════════════════
set -euo pipefail

TARGET=${1:-student}
SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
LAB_DIR="$SCRIPT_DIR/.."
SRC_DIR="$LAB_DIR/$TARGET"
PORT=9001
SERVER_PID=""

# ─── 颜色 ────────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; NC='\033[0m'; BOLD='\033[1m'

info()  { echo -e "${CYAN}[INFO]${NC} $*"; }
ok()    { echo -e "${GREEN}[PASS]${NC} $*"; }
fail()  { echo -e "${RED}[FAIL]${NC} $*"; }
title() { echo -e "\n${BOLD}${YELLOW}$*${NC}"; }

# ─── 清理 ────────────────────────────────────────────────────────────────────
cleanup() {
  if [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
    info "停止服务器（PID=$SERVER_PID ）..."
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT INT TERM

# ─── 步骤 1：编译 ─────────────────────────────────────────────────────────────
title "═══ BattleWorld Lab2 自动测试 ═══"
info "目标目录：$SRC_DIR"

info "普通编译检查..."
if ! (cd "$SRC_DIR" && go build ./... 2>&1); then
  fail "编译失败，请检查代码！"
  exit 1
fi
ok "普通编译通过"

# 尝试 -race 编译（检测数据竞争）
USE_RACE=""
info "尝试 -race 编译（数据竞争检测）..."
if (cd "$SRC_DIR" && go build -race ./... 2>&1); then
  ok "-race 编译通过"
  USE_RACE="-race"
else
  info "-race 编译失败，使用普通模式（建议检查锁的使用）"
fi

# ─── 步骤 2：启动服务器（只启动一次）────────────────────────────────────────
title "启动服务器..."

# 先清理端口
# PID=$(lsof -ti :$PORT)

# if [ -n "$PID" ]; then
#   info "检测到端口 $PORT 被占用，正在清理..."
#   kill -9 $PID
#   sleep 1
# fi

# 再等待端口真正释放
for i in $(seq 1 10); do
  if ! lsof -i ":$PORT" >/dev/null 2>&1; then
    break
  fi
  sleep 0.2
done

# 启动服务器，输出带前缀方便阅读
if [ -n "$USE_RACE" ]; then
  (cd "$SRC_DIR" && go run -race ./cmd/server 2>&1 | sed 's/^/  [server] /') &
else
  (cd "$SRC_DIR" && go run ./cmd/server 2>&1 | sed 's/^/  [server] /') &
fi
SERVER_PID=$!

# 等待服务器监听成功（最多 10s）
SERVER_READY=false
for i in $(seq 1 40); do
  if nc -z 127.0.0.1 "$PORT" 2>/dev/null; then
    SERVER_READY=true
    break
  fi
  sleep 0.25
done

if ! $SERVER_READY; then
  fail "服务器未能在 10s 内启动，请检查代码"
  exit 1
fi
ok "服务器已就绪（ PID=$SERVER_PID ） "

# ─── 步骤 3：逐项运行测试 ──────────────────────────────────────────────────────
echo ""
TOTAL_PASS=0
TOTAL_FAIL=0

run_case() {
  local tid="$1"
  local name="$2"
  title "【Test $tid 】$name"

  # 测试间隔：给服务器时间处理上一批连接的断开清理
  sleep 0.5

  local result=0
  (cd "$SCRIPT_DIR" && go run autotest.go "$tid") || result=$?

  if [ $result -eq 0 ]; then
    TOTAL_PASS=$((TOTAL_PASS + 1))
    ok "Test $tid 通过"
  else
    TOTAL_FAIL=$((TOTAL_FAIL + 1))
    fail "Test $tid 失败"
  fi
}

run_case 1 "多客户端并发连接（任务 D-2）"
run_case 2 "AddPlayer 并发安全：ID 唯一（任务 C-1）"
run_case 3 "RemovePlayer 正确性（任务 C-2）"
run_case 4 "MovePlayer 边界检查（任务 C-3）"
run_case 5 "GetSnapshot 并发读安全（任务 C-5）"
run_case 6 "AttackPlayer 伤害计算与死亡判断（任务 C-4）"
run_case 7 "广播 Goroutine 定期推送（任务 D-1）"

# ─── 步骤 4：DATA RACE 检查提示 ────────────────────────────────────────────────
echo ""
if [ -n "$USE_RACE" ]; then
  # 检查服务器 stderr/stdout 中是否出现 DATA RACE
  # 因为输出通过管道走了，这里只给出提示
  echo -e "${CYAN}[INFO]${NC} 请检查上方 [server] 输出，若出现 'WARNING: DATA RACE' 说明锁未正确加。"
fi

# ─── 步骤 5：汇总 ──────────────────────────────────────────────────────────────
echo ""
echo -e "${BOLD}═══════════════════════════════════════════════════${NC}"
echo -e "  测试汇总：${GREEN}${TOTAL_PASS} 通过${NC}，${RED}${TOTAL_FAIL} 失败${NC}"
echo -e "${BOLD}═══════════════════════════════════════════════════${NC}"

if [ "$TOTAL_FAIL" -eq 0 ]; then
  echo -e "\n${GREEN}🎉 全部通过！实验 Lab2 完成！${NC}\n"
  exit 0
else
  echo -e "\n${RED}❌ 有测试未通过，请根据上方输出定位问题。${NC}\n"
  exit 1
fi
