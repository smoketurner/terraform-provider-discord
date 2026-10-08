default: fmt lint build test

build:
	go build -v ./...

install: build
	go install -v ./...

lint:
	golangci-lint run ./...

fmt:
	gofmt -s -w -e .
	terraform fmt -recursive examples/

generate:
	cd tools && go generate -tags generate ./...

# Runs every test, including acceptance tests, against the in-memory fake
# Discord API. Requires terraform on PATH.
test:
	go test -cover -timeout=10m ./...

# Runs acceptance tests against a real Discord server. Requires TF_ACC=1,
# DISCORD_TOKEN, DISCORD_SERVER_ID and optionally DISCORD_TEST_USER_ID.
testacc:
	TF_ACC=1 go test -v -cover -timeout 60m ./internal/provider/

# Checks coverage.yaml and the client models against the pinned Discord
# OpenAPI spec, which it downloads first.
API_SPEC ?= .openapi.json
api-coverage:
	go run ./internal/apispec/cmd/apispec fetch -o $(API_SPEC)
	DISCORD_API_SPEC=$(abspath $(API_SPEC)) go test ./internal/apispec/...

.PHONY: default build install lint fmt generate test testacc api-coverage
