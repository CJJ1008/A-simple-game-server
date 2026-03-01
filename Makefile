# ============================================================
# Makefile  —  多人对战游戏 v3.0
# ============================================================

CXX      = g++
CXXFLAGS = -std=c++17 -O2 -Wall -Wextra -pthread

.PHONY: all clean server client

all: server client

server: server.cpp protocol.h database.h
	$(CXX) $(CXXFLAGS) -o server server.cpp
	@echo "✓ server 编译成功"

client: client.cpp protocol.h
	$(CXX) $(CXXFLAGS) -o client client.cpp
	@echo "✓ client 编译成功"

clean:
	rm -f server client
	@echo "✓ 清理完成"