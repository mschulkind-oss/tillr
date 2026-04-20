# Installation

Tillr is a single Go binary. Build from source:

```bash
cd /path/to/tillr
just build          # or: go build -o tillr ./cmd/tillr
```

Place the resulting `tillr` binary somewhere on your `$PATH`.

Verify the install:

```bash
tillr doctor
```

## Requirements

- **Go 1.24+** (build only)
- **SQLite** (bundled via `modernc.org/sqlite`; pure Go, no CGO or external install needed)
- A modern browser for the web viewer

---

« [User Guide](./README.md)
