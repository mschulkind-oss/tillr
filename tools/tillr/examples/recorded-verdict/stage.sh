#!/bin/sh
set -eu
dir=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
export TILLR_EXAMPLE_DIR="$dir"
exec go run "$dir/stage.go" "$@"
