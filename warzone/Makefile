.PHONY: all server client clean deps

all: server client

deps:
	go mod tidy

server:
	go build -o server ./cmd/server

client:
	go build -o client ./cmd/client

clean:
	rm -f server client
