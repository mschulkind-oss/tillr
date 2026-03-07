#!/usr/bin/env bash
set -euo pipefail

echo "🚀 Bootstrapping Lifecycle self-management..."

# Build if needed
if [ ! -f bin/lifecycle ]; then
    echo "Building lifecycle..."
    go build -o bin/lifecycle ./cmd/lifecycle
fi

# Initialize project if not exists
if [ ! -f .lifecycle.db ]; then
    echo "Initializing lifecycle project..."
    bin/lifecycle init lifecycle --description "Human-in-the-loop project management for agentic development"
fi

# Check if already bootstrapped
FEATURE_COUNT=$(bin/lifecycle feature list --json 2>/dev/null | python3 -c "import sys,json; print(len(json.load(sys.stdin)))" 2>/dev/null || echo "0")
if [ "$FEATURE_COUNT" -gt 0 ]; then
    echo "Already bootstrapped with $FEATURE_COUNT features."
    echo "Run 'bin/lifecycle status' to see project overview."
    exit 0
fi

echo "Creating milestones..."
bin/lifecycle milestone add "v0.1 MVP" --description "Core CLI and web viewer"
bin/lifecycle milestone add "v0.2 Self-Hosting" --description "Agent coordination and self-management"
bin/lifecycle milestone add "v1.0 Production" --description "Full-featured release"

echo "✅ Bootstrap complete! Run 'bin/lifecycle serve' to start the web viewer."
