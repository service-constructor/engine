GOBIN := $(shell go env GOPATH)/bin
export PATH := $(PATH):$(GOBIN)

.PHONY: tools generate tidy build run docker-up docker-down migrate-up migrate-down test

# Install codegen plugins pinned alongside the module.
tools:
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@latest

# Generate Go stubs, gRPC server and HTTP gateway from proto.
generate:
	buf generate --path proto/serviceconstructor

tidy:
	go mod tidy

build:
	go build -o bin/server ./cmd/server

run:
	go run ./cmd/server

docker-up:
	docker compose -f deploy/docker-compose.yml up -d

docker-down:
	docker compose -f deploy/docker-compose.yml down

test:
	go test ./...
