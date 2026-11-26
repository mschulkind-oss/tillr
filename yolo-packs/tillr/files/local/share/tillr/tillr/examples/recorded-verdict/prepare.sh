#!/bin/sh
# Resolve program paths in the copied example; never open a store or invoke Git.
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
mkdir -p "$root/config/tillr"
cat >"$root/config/tillr/backend-policy.json" <<EOF
{
  "default_backend": "stub",
  "seats": {
    "build": "stub"
  },
  "backends": {
    "stub": {
      "adapter": "$root/stub-adapter.sh",
      "model": "deterministic-example"
    }
  }
}
EOF
cat >"$root/config/tillr/commands.json" <<EOF
{
  "reproduce": {
    "argv": [
      "$root/stage.sh",
      "reproduce"
    ],
    "flags": {}
  }
}
EOF
