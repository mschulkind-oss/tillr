#!/bin/sh
# A deterministic stand-in for an agent backend; ignores model routing arguments.
set -eu
exec python3 "$(dirname "$0")/stage.py" investigate
