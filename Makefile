.PHONY: tidy fmt build run gen new_migration docker oui asn geo world htmx test lint dev e2e e2e-full

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

asn: ## Rebuild the embedded IP-to-ASN tables from DB-IP.
	cd pkg/asn && go run ./internal/gen

geo: ## Rebuild the embedded IP-to-country table from DB-IP.
	cd pkg/geo && go run ./internal/gen

world: ## Rebuild the embedded world outline from Natural Earth.
	cd pkg/geo && go run ./internal/world

# htmx is vendored because the content security policy admits scripts from
# this origin only.
HTMX_VERSION ?= 2.0.8

htmx: ## Refresh the vendored htmx. Usage: make htmx [HTMX_VERSION=2.0.8]
	curl -sfL -o internal/web/statics/js/htmx.min.js \
		https://cdnjs.cloudflare.com/ajax/libs/htmx/$(HTMX_VERSION)/htmx.min.js

test:
	go test ./...

# The browser tests write databases and Chrome profiles under TMPDIR; a full
# run is larger than some /tmp partitions, so they go under tmp/ here.
E2E_TMP := $(CURDIR)/tmp/e2e

e2e: ## Run the browser tests a pull request must pass. Needs Chrome.
	mkdir -p $(E2E_TMP)
	TMPDIR=$(E2E_TMP) go test -tags e2e -count=1 -timeout 30m -run 'TestFixturesRender|TestE2E|TestSearchKeepsFocus' ./internal/web/e2e/

e2e-full: ## Run the browser tests on every screen and account, as a release does.
	mkdir -p $(E2E_TMP)
	E2E_FULL=1 TMPDIR=$(E2E_TMP) go test -tags e2e -count=1 -timeout 60m -run 'TestFixturesRender|TestE2E|TestSearchKeepsFocus' ./internal/web/e2e/

lint: ## Run golangci-lint
	@if [ ! -f ./bin/golangci-lint ]; then \
		curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b ./bin v2.13.2; \
	fi
	./bin/golangci-lint run ./...

dev: gen build ## Start the server with hot-reload
	go tool air