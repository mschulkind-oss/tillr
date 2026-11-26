#!/bin/sh
# A deterministic stand-in for an agent backend; ignores model routing arguments.
set -eu
exec sh "$(dirname "$0")/stage.sh" investigate
