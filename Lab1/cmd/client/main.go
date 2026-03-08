package main

import (
	"flag"
	"log"

	"cs/internal/client"
	"cs/internal/client/tui"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:7777", "server address")
	name := flag.String("name", "player", "player name")
	flag.Parse()

	backend := client.NewNetBackend()
	app := tui.New(backend)
	if err := app.Run(*addr, *name); err != nil {
		log.Fatal(err)
	}
}
