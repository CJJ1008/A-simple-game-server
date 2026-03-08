package main

import (
	"warzone/internal/client"
	"fmt"
	"os"
	"strconv"
)

const (
	defaultHost = "127.0.0.1"
	defaultPort = 9000
)

func main() {
	host := defaultHost
	port := defaultPort

	if len(os.Args) >= 2 {
		host = os.Args[1]
	}
	if len(os.Args) >= 3 {
		p, err := strconv.Atoi(os.Args[2])
		if err != nil {
			fmt.Fprintf(os.Stderr, "无效端口: %s\n", os.Args[2])
			os.Exit(1)
		}
		port = p
	}

	if err := client.Run(host, port); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
