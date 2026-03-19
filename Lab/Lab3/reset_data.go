package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "获取当前目录失败：%v\n", err)
		os.Exit(1)
	}

	if err := resetOne(filepath.Join(root, "complete")); err != nil {
		fmt.Fprintf(os.Stderr, "重置 complete 数据失败：%v\n", err)
		os.Exit(1)
	}
	if err := resetOne(filepath.Join(root, "student")); err != nil {
		fmt.Fprintf(os.Stderr, "重置 student 数据失败：%v\n", err)
		os.Exit(1)
	}

	fmt.Println("Lab3 数据已清空：complete 与 student 的账号、会话、检查点都已重置。")
}

func resetOne(base string) error {
	coldDir := filepath.Join(base, "data", "cold")
	hotDir := filepath.Join(base, "data", "hot")
	checkpointDir := filepath.Join(hotDir, "checkpoints")

	if err := os.MkdirAll(coldDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(checkpointDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(coldDir, "users.json"), []byte("{}\n"), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(hotDir, "sessions.json"), []byte("{}\n"), 0o644); err != nil {
		return err
	}

	entries, err := os.ReadDir(checkpointDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		if err := os.Remove(filepath.Join(checkpointDir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}
