#!/usr/bin/env bash
set -euo pipefail

echo "🚀 Bootstrapping Lifecycle self-management..."

# Build if needed
if [ ! -f bin/tillr ]; then
    echo "Building tillr..."
    go build -o bin/tillr ./cmd/tillr
fi

# Initialize project if not exists
if [ ! -f .tillr.db ]; then
    echo "Initializing tillr project..."
    bin/tillr init tillr --description "Human-in-the-loop project management for agentic development"
fi

# Check if already bootstrapped
FEATURE_COUNT=$(bin/tillr feature list --json 2>/dev/null | python3 -c "import sys,json; print(len(json.load(sys.stdin)))" 2>/dev/null || echo "0")
if [ "$FEATURE_COUNT" -gt 0 ]; then
    echo "Already bootstrapped with $FEATURE_COUNT features."
    echo "Run 'bin/tillr status' to see project overview."
    exit 0
fi

echo "Creating milestones..."
bin/tillr milestone add "v0.1 MVP" --description "Core CLI and web viewer"
bin/tillr milestone add "v0.2 Self-Hosting" --description "Agent coordination and self-management"
bin/tillr milestone add "v1.0 Production" --description "Full-featured release"

echo "✅ Bootstrap complete! Run 'bin/tillr serve' to start the web viewer."
