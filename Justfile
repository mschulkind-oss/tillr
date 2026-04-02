default:
    @just --list

# Build the tillr binary
build:
    cd web && pnpm build
    go build -ldflags "-X github.com/mschulkind-oss/tillr/internal/version.Version=dev-$(git rev-parse --short HEAD) -X github.com/mschulkind-oss/tillr/internal/version.GitCommit=$(git rev-parse --short HEAD)" -o bin/tillr ./cmd/tillr

# Run tillr CLI with args
run *args:
    go run ./cmd/tillr -- {{args}}

# The universal quality gate (auto-fixes formatting)
check: format lint test

# Read-only quality gate (used by pre-commit hook and CI)
check-ci: lint-ci test

# Format all Go code
format:
    gofmt -w .
    goimports -w .

# Lint with golangci-lint (auto-fix)
lint:
    golangci-lint run ./... --fix

# Lint without auto-fix (CI mode)
lint-ci:
    gofmt -l . | xargs -r false  # fail if any files need formatting
    golangci-lint run ./...

# Run all tests
test *args:
    go test ./... -v -count=1 {{args}}

# Run tests with coverage
test-cov:
    go test ./... -v -coverprofile=coverage.out
    go tool cover -html=coverage.out -o coverage.html

# Full dev environment: Go backend (air live-reload) + Vite frontend (HMR)
# Runs daemonized via overmind — no dedicated terminal needed.
# Auto-detects jail vs host and picks non-colliding ports.
# Inside jail:  backend=3847  frontend=3848  (both forwarded by yolo-jail)
# On host:      backend=3850  frontend=5173  (avoids jail port-forwarding)
dev:
    #!/usr/bin/env bash
    set -euo pipefail
    if [ -f /run/.containerenv ] || [ -f /.dockerenv ]; then
        BACKEND_PORT=3847
        FRONTEND_PORT=3848
    else
        BACKEND_PORT=3850
        FRONTEND_PORT=5173
    fi
    export TILLR_PORT="$BACKEND_PORT"
    export VITE_PORT="$FRONTEND_PORT"
    if command -v air &>/dev/null; then
        printf 'backend: air -- serve --port %s\nfrontend: cd web && TILLR_PORT=%s VITE_PORT=%s pnpm dev\n' \
            "$BACKEND_PORT" "$BACKEND_PORT" "$FRONTEND_PORT" > /tmp/Procfile.tillr
    else
        echo "⚠  air not found — backend won't live-reload (install: go install github.com/air-verse/air@latest)"
        printf 'backend: go run ./cmd/tillr serve --port %s\nfrontend: cd web && TILLR_PORT=%s VITE_PORT=%s pnpm dev\n' \
            "$BACKEND_PORT" "$BACKEND_PORT" "$FRONTEND_PORT" > /tmp/Procfile.tillr
    fi
    overmind start -f /tmp/Procfile.tillr -D
    echo "Dev environment started (overmind, daemonized)"
    echo "  Backend:  http://localhost:$BACKEND_PORT"
    echo "  Frontend: http://localhost:$FRONTEND_PORT"
    echo ""
    echo "  just dev-logs         # tail all logs"
    echo "  just dev-stop         # stop everything"
    echo "  just dev-restart      # restart everything"

# Tail dev environment logs
dev-logs:
    overmind echo

# Stop dev environment
dev-stop:
    overmind quit

# Restart dev environment
dev-restart: dev-stop dev

# Start Go backend only (with live reload if air is installed)
dev-backend port="3847":
    #!/usr/bin/env bash
    set -euo pipefail
    export TILLR_PORT={{port}}
    if command -v air &>/dev/null; then
        air -- serve --port {{port}}
    else
        echo "Install air for live reload: go install github.com/air-verse/air@latest"
        echo "Falling back to plain go run..."
        go run ./cmd/tillr serve --port {{port}}
    fi

# Start Vite dev server only (proxies API to Go server on given port)
dev-frontend port="3847":
    cd web && TILLR_PORT={{port}} pnpm dev

# Start Go server without live reload (for production-like testing)
serve port="3847":
    TILLR_PORT={{port}} go run ./cmd/tillr serve --port {{port}}

# Install the binary locally
install:
    cd web && pnpm build
    go install ./cmd/tillr

# Install systemd user service
install-service:
    mkdir -p ~/.config/systemd/user
    cp tillr.service ~/.config/systemd/user/tillr.service
    systemctl --user daemon-reload
    systemctl --user enable tillr
    @echo "Service installed. Start with: just restart-service"

# Deploy: build, install, restart
deploy: install
    systemctl --user restart tillr
    @echo "Tillr deployed and service restarted"

# Restart the systemd user service
restart-service:
    systemctl --user restart tillr

# Show service status
status:
    systemctl --user status tillr

# Follow service logs
logs:
    journalctl --user -u tillr -f

# Clean build artifacts
clean:
    rm -rf bin/ coverage.out coverage.html

# Docker build
docker-build:
    docker build -t tillr:latest .

# Docker run with local DB
docker-run:
    docker run -p 3847:3847 -v $(pwd)/.tillr.db:/app/.tillr.db tillr:latest

# Bootstrap self-management
bootstrap:
    bash scripts/bootstrap.sh
