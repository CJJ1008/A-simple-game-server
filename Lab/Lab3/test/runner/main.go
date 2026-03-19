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

var ports = []int{9310, 9311, 9312, 9313}

func main() {
	target := "student"
	args := os.Args[1:]
	if len(args) > 0 && (args[0] == "student" || args[0] == "complete") {
		target = args[0]
		args = args[1:]
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

	if occupied := occupiedPorts(); len(occupied) > 0 {
		fmt.Fprintf(os.Stderr, "检测到端口被占用，无法启动 %s 服务：%v\n", target, occupied)
		os.Exit(1)
	}

	dataRoot, err := os.MkdirTemp("", "lab3_test_data.")
	if err != nil {
		fmt.Fprintf(os.Stderr, "创建临时数据目录失败：%v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(dataRoot)

	server, logs, err := startServer(srcDir, cacheDir, dataRoot, 10*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s 服务启动失败：%v\n", target, err)
		if logs.Len() > 0 {
			fmt.Fprintln(os.Stderr, "服务端日志：")
			fmt.Fprint(os.Stderr, logs.String())
		}
		os.Exit(1)
	}
	defer stopServer(server)

	if err := runAutotest(testDir, cacheDir, args...); err != nil {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "%s 服务端日志如下：\n\n", target)
		fmt.Fprint(os.Stderr, logs.String())
		os.Exit(1)
	}
}

func startServer(srcDir, cacheDir, dataRoot string, timeout time.Duration) (*exec.Cmd, *bytes.Buffer, error) {
	cmd := exec.Command("go", "run", "./cmd/server")
	cmd.Dir = srcDir
	cmd.Env = append(withGoCache(cacheDir), "LAB3_DATA_ROOT="+dataRoot)
	var logs bytes.Buffer
	cmd.Stdout = &logs
	cmd.Stderr = &logs
	if err := cmd.Start(); err != nil {
		return nil, &logs, err
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if canDial(9310) {
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

func runAutotest(testDir, cacheDir string, args ...string) error {
	cmdArgs := append([]string{"run", "autotest.go"}, args...)
	cmd := exec.Command("go", cmdArgs...)
	cmd.Dir = testDir
	cmd.Env = withGoCache(cacheDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func occupiedPorts() []int {
	var occupied []int
	for _, port := range ports {
		if canDial(port) {
			occupied = append(occupied, port)
		}
	}
	return occupied
}

func canDial(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
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
