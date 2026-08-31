.PHONY: tidy fmt build run gen new_migration docker oui test lint

.DEFAULT_GOAL := build

fmt:
	go fmt ./... && go tool goimports -w .

gen:
	go generate ./...

build: gen
	go build -o bin/ ./cmd/jocasta

run: build
	./bin/jocasta

docker: ## Build the container image. Usage: make docker [tag=<image tag>]
	docker build -t $(or $(tag),jocasta:latest) .


new_migration: ## Create a new migration file. Usage: make new_migration name=<migration_name>
	go tool migrate create -dir=internal/db/migrations/ -seq -ext sql $(name)

oui: ## Rebuild the embedded MAC vendor table from IEEE and Wireshark.
	cd pkg/oui && go run ./internal/gen

test:
	go test ./...

lint: ## Run golangci-lint
	@if [ ! -f ./bin/golangci-lint ]; then \
		curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b ./bin v2.13.2; \
	fi
	./bin/golangci-lint run ./...