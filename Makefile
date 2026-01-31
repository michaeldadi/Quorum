.PHONY: build run test lint fmt clean test-race test-cover test-chaos bench docker

BINARY=bin/quorum
VERSION?=dev

build:
	go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY) ./cmd/quorum

run: build
	./$(BINARY)

test:
	go test -v -short ./...

test-race:
	go test -v -race -short ./...

test-cover:
	go test -v -race -short -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

test-chaos:
	go test -v -race -timeout 10m ./internal/chaos/

bench:
	go test -bench=. -benchtime=5s ./internal/bench/

lint:
	golangci-lint run

fmt:
	gofmt -s -w .
	go mod tidy

clean:
	rm -rf bin/ data/ coverage.out coverage.html
	go clean -testcache

# Docker
docker:
	docker build -t quorum:$(VERSION) .

docker-up:
	docker-compose up -d

docker-down:
	docker-compose down

docker-logs:
	docker-compose logs -f

# Local cluster
cluster:
	@echo "Starting 3-node cluster..."
	@./$(BINARY) --id=node-1 --port=9001 --http=8001 &
	@./$(BINARY) --id=node-2 --port=9002 --http=8002 &
	@./$(BINARY) --id=node-3 --port=9003 --http=8003 &
	@echo "Cluster started. Use 'pkill quorum' to stop."

# Demo
demo: build
	@echo "=== Starting cluster ==="
	@$(MAKE) cluster
	@sleep 3
	@echo ""
	@echo "=== Cluster status ==="
	@curl -s http://localhost:8001/status | jq .
	@echo ""
	@echo "=== Writing data ==="
	@curl -s -X POST http://localhost:8001/put -H "Content-Type: application/json" -d '{"key":"demo","value":"hello from quorum"}' | jq .
	@echo ""
	@echo "=== Reading from all nodes ==="
	@echo "Node 1:" && curl -s "http://localhost:8001/get?key=demo" | jq .
	@echo "Node 2:" && curl -s "http://localhost:8002/get?key=demo&local=true" | jq .
	@echo "Node 3:" && curl -s "http://localhost:8003/get?key=demo&local=true" | jq .
