package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const port = 9001

func main() {
	target := "complete"
	if len(os.Args) > 1 && os.Args[1] != "" {
		target = os.Args[1]
	}

	testDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "获取当前目录失败：%v\n", err)
		os.Exit(1)
	}
	labDir := filepath.Clean(filepath.Join(testDir, ".."))
	srcDir := filepath.Join(labDir, target)
	cacheDir := filepath.Join(labDir, ".gocache")
	_ = os.MkdirAll(cacheDir, 0o755)

	fmt.Println("═══ BattleWorld Lab2 自动测试 ═══")
	fmt.Printf("目标目录：%s\n", srcDir)

	if err := runGo(srcDir, cacheDir, "build", "./..."); err != nil {
		fmt.Fprintf(os.Stderr, "普通编译失败：%v\n", err)
		os.Exit(1)
	}
	fmt.Println("普通编译通过")

	useRace := false
	if err := runGo(srcDir, cacheDir, "build", "-race", "./..."); err == nil {
		useRace = true
		fmt.Println("-race 编译通过")
	} else {
		fmt.Println("-race 编译不可用，改用普通模式")
	}

	server, logs, err := startServer(srcDir, cacheDir, useRace, 10*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "启动服务器失败：%v\n", err)
		if logs.Len() > 0 {
			fmt.Fprintln(os.Stderr, "服务端日志：")
			fmt.Fprint(os.Stderr, logs.String())
		}
		os.Exit(1)
	}
	defer stopServer(server)

	tests := []struct {
		id   string
		name string
	}{
		{"1", "多客户端并发连接（任务 D-2）"},
		{"2", "AddPlayer 并发安全：ID 唯一（任务 C-1）"},
		{"3", "RemovePlayer 正确性（任务 C-2）"},
		{"4", "MovePlayer 边界检查（任务 C-3）"},
		{"5", "GetSnapshot 并发读安全（任务 C-5）"},
		{"6", "AttackPlayer 伤害计算与死亡判断（任务 C-4）"},
		{"7", "广播 Goroutine 定期推送（任务 D-1）"},
	}

	passed := 0
	failed := 0
	for _, tc := range tests {
		fmt.Printf("\n【Test %s】%s\n", tc.id, tc.name)
		time.Sleep(500 * time.Millisecond)
		if err := runAutotest(testDir, cacheDir, tc.id); err != nil {
			failed++
		} else {
			passed++
		}
	}

	fmt.Println()
	fmt.Println("═══════════════════════════════════════════════════")
	fmt.Printf("  测试汇总：%d 通过，%d 失败\n", passed, failed)
	fmt.Println("═══════════════════════════════════════════════════")
	if useRace {
		fmt.Println("提示：若上方输出出现 'WARNING: DATA RACE'，说明仍存在竞争问题。")
	}
	if failed > 0 {
		fmt.Fprintln(os.Stderr, "服务端日志：")
		fmt.Fprint(os.Stderr, logs.String())
		os.Exit(1)
	}
}

func runGo(dir, cacheDir string, args ...string) error {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = withGoCache(cacheDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func startServer(srcDir, cacheDir string, useRace bool, timeout time.Duration) (*exec.Cmd, *bytes.Buffer, error) {
	args := []string{"run"}
	if useRace {
		args = append(args, "-race")
	}
	args = append(args, "./cmd/server")

	cmd := exec.Command("go", args...)
	cmd.Dir = srcDir
	cmd.Env = withGoCache(cacheDir)
	var logs bytes.Buffer
	cmd.Stdout = &logs
	cmd.Stderr = &logs
	if err := cmd.Start(); err != nil {
		return nil, &logs, err
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if canDial(port) {
			return cmd, &logs, nil
		}
		time.Sleep(250 * time.Millisecond)
	}

	stopServer(cmd)
	return nil, &logs, fmt.Errorf("服务器未能在 %s 内启动", timeout)
}

func stopServer(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
}

func runAutotest(testDir, cacheDir, tid string) error {
	cmd := exec.Command("go", "run", "autotest.go", tid)
	cmd.Dir = testDir
	cmd.Env = withGoCache(cacheDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func canDial(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 250*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func withGoCache(cacheDir string) []string {
	env := os.Environ()
	return append(env, "GOCACHE="+cacheDir)
}
