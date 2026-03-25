.PHONY: cli server dev-server clean

cli:
	go build -o bin/pt ./cmd/pt

server:
	go build -o bin/pt-server ./server

dev-server:
	go run ./server

clean:
	rm -rf bin/
