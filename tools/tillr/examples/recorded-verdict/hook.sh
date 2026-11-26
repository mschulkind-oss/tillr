#!/bin/sh
set -eu
exec go run "$(dirname "$0")/hook.go" "$@"
