.PHONY: build test

build:
	go build -o bin/stackpilot ./cmd/stackpilot

test:
	go vet ./...
	go test ./...
