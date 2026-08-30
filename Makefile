.PHONY: tidy fmt build run gen new_migration

fmt:
	go fmt ./... && go tool goimports -w .

gen:
	go generate ./...

build: gen
	go build -o bin/ ./cmd/jocasta

run: build
	./bin/jocasta


new_migration: ## Create a new migration file. Usage: make new_migration name=<migration_name>
	go tool migrate create -dir=internal/db/migrations/ -seq -ext sql $(name)
