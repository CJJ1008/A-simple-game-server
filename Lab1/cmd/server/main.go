package main

import (
	"flag"
	"io"
	"log"
	"os"

	"cs/internal/server"
)

func main() {
	addr := flag.String("addr", ":7777", "listen address")
	mapW := flag.Int("w", 20, "map width")
	mapH := flag.Int("h", 10, "map height")
	dbPath := flag.String("db", "game.db", "sqlite db path")
	logOutput := flag.String("log", "stdout", "log output: stdout or file path")
	flag.Parse()

	var out io.Writer = os.Stdout
	var logFile *os.File
	if *logOutput != "stdout" {
		f, err := os.OpenFile(*logOutput, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			log.Fatal(err)
		}
		logFile = f
		out = f
	}
	if logFile != nil {
		defer func() {
			_ = logFile.Close()
		}()
	}

	logger := log.New(out, "server ", log.LstdFlags)

	store, err := server.NewSqliteStore(*dbPath)
	if err != nil {
		log.Fatal(err)
	}

	srv := server.New(*addr, *mapW, *mapH, store, logger)
	logger.Printf("server listening on %s", *addr)
	if err := srv.Start(); err != nil {
		logger.Fatal(err)
	}
}
