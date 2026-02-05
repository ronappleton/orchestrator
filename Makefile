.PHONY: build run test

build:
	go build -o bin/orchestrator ./cmd/orchestrator

run: build
	./bin/orchestrator -config config.yaml

test:
	go test ./...
