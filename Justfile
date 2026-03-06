default:
    @just --list

# Build the lifecycle binary
build:
    go build -o bin/lifecycle ./cmd/lifecycle

# Run lifecycle CLI with args
run *args:
    go run ./cmd/lifecycle -- {{args}}

# The universal quality gate
check: format lint test

# Format all Go code
format:
    gofmt -w .
    goimports -w .

# Lint with golangci-lint
lint:
    golangci-lint run ./...

# Run all tests
test:
    go test ./... -v -count=1

# Run tests with coverage
test-cov:
    go test ./... -v -coverprofile=coverage.out
    go tool cover -html=coverage.out -o coverage.html

# Start the web viewer dev server
dev:
    go run ./cmd/lifecycle serve

# Install the binary locally
install:
    go install ./cmd/lifecycle

# Clean build artifacts
clean:
    rm -rf bin/ coverage.out coverage.html

# Push jj bookmarks to remotes
push:
    jj git push --bookmark main --remote public
    jj git push --bookmark main --bookmark dev --bookmark staging --remote private

# Pre-promote quality gate
prepromote: check
    @echo "All checks passed — ready to promote"

# Promote staging to main
promote: prepromote
    #!/usr/bin/env bash
    set -euo pipefail
    jj bookmark set main -r staging
    jj new staging
    jj bookmark set staging -r @
    jj new
    jj bookmark set dev -r @
    just push
    @echo "Promoted staging → main, created fresh staging + dev"
