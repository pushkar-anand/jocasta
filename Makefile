.PHONY: tidy fmt build run

fmt:
	go fmt ./... && go list ./... | goimports -w .

build:
	go build -o bin/ ./cmd/jocasta
