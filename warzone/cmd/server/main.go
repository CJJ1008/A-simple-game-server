package main

import (
	"warzone/internal/server"
	"fmt"
	"os"
	"strconv"
)

const defaultPort = 9000

func main() {
	port := defaultPort
	if len(os.Args) >= 2 {
		p, err := strconv.Atoi(os.Args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "无效端口: %s\n", os.Args[1])
			os.Exit(1)
		}
		port = p
	}

	srv := server.New("data")
	if err := srv.Start(port); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
