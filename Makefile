.PHONY: tidy fmt build run gen new_migration docker oui htmx test lint dev

.DEFAULT_GOAL := build

fmt:
	go fmt ./... && go tool goimports -w .

gen:
	go generate ./...

build:
	go build -o bin/ ./cmd/jocasta

run: gen build
	./bin/jocasta

docker: ## Build the container image. Usage: make docker [tag=<image tag>]
	docker build -t $(or $(tag),jocasta:latest) .


new_migration: ## Create a new migration file. Usage: make new_migration name=<migration_name>
	go tool migrate create -dir=internal/db/migrations/ -seq -ext sql $(name)

oui: ## Rebuild the embedded MAC vendor table from IEEE and Wireshark.
	cd pkg/oui && go run ./internal/gen

# htmx is vendored rather than loaded from a CDN because the content security
# policy admits scripts from this origin only.
HTMX_VERSION ?= 2.0.8

htmx: ## Refresh the vendored htmx. Usage: make htmx [HTMX_VERSION=2.0.8]
	curl -sfL -o internal/web/statics/js/htmx.min.js \
		https://cdnjs.cloudflare.com/ajax/libs/htmx/$(HTMX_VERSION)/htmx.min.js

test:
	go test ./...

lint: ## Run golangci-lint
	@if [ ! -f ./bin/golangci-lint ]; then \
		curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b ./bin v2.13.2; \
	fi
	./bin/golangci-lint run ./...

dev: gen build ## Start the server with hot-reload
	go tool air