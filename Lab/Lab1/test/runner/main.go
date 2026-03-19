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

const port = 9000

func main() {
	target := "student"
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

	fmt.Println("═══ BattleWorld Lab1 自动测试 ═══")
	fmt.Printf("目标目录：%s\n", srcDir)

	if err := runGo(srcDir, cacheDir, "build", "./cmd/server"); err != nil {
		fmt.Fprintf(os.Stderr, "服务器编译失败：%v\n", err)
		os.Exit(1)
	}
	fmt.Println("编译成功")

	tests := []struct {
		id   string
		name string
	}{
		{"1", "连接与握手（任务 A：Send/Receive）"},
		{"2", "移动上边界保护（任务 B-1：handleMove）"},
		{"3", "移动方向正确性（任务 B-1：handleMove）"},
		{"4", "超出攻击范围（任务 B-2：handleAttack）"},
		{"5", "攻击距离检测（任务 B-2：handleAttack）"},
		{"6", "药水治疗（handleHeal，已提供参考）"},
		{"7", "断线游戏结束（综合）"},
	}

	passed := 0
	failed := 0
	for _, tc := range tests {
		fmt.Printf("\n【Test %s】%s\n", tc.id, tc.name)
		server, logs, err := startServer(srcDir, cacheDir, 6*time.Second)
		if err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "启动服务器失败：%v\n", err)
			if logs.Len() > 0 {
				fmt.Fprintln(os.Stderr, "服务端日志：")
				fmt.Fprint(os.Stderr, logs.String())
			}
			continue
		}

		err = runAutotest(testDir, cacheDir, tc.id)
		stopServer(server)
		if err != nil {
			failed++
		} else {
			passed++
		}
	}

	fmt.Println()
	fmt.Println("═══════════════════════════════════════════════════")
	fmt.Printf("  测试汇总：%d 通过，%d 失败\n", passed, failed)
	fmt.Println("═══════════════════════════════════════════════════")
	if failed > 0 {
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

func startServer(srcDir, cacheDir string, timeout time.Duration) (*exec.Cmd, *bytes.Buffer, error) {
	cmd := exec.Command("go", "run", "./cmd/server")
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
		time.Sleep(300 * time.Millisecond)
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
	time.Sleep(200 * time.Millisecond)
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
