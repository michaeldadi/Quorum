.PHONY: build run test lint fmt clean

BINARY=bin/quorum

build:
	go build -o $(BINARY) ./cmd/quorum

run: build
	./$(BINARY)

test:
	go test -v -race -cover ./...

lint:
	golangci-lint run

fmt:
	gofmt -s -w .
	go mod tidy

clean:
	rm -rf bin/
	go clean -testcache

# Run a 3-node cluster locally (we'll use this later)
cluster:
	@echo "TODO: start 3-node local cluster"
