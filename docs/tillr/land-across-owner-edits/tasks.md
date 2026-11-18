# Tasks: landing across the owner's uncommitted edits

Stage 3.

| Task | Where |
| :--- | :--- |
| prediction and recovery copy | `tools/tillr-land/land/owner.go` |
| no-follow install, exchange rename, undo, last read | `tools/tillr-land/land/owner_fs.go`, `owner_install.go` |
| prepare/transaction split, retry, fault events | `tools/tillr-land/land/landing.go`, `schema.go` |
| final-move marker `-c tillr.move=final` for the stall wrappers | `contention_test.go`, `idempotent_test.go` |
| semantic wait check | `tools/tillr-drain/drain/dirty_hold.go` |
| boundary exemption | `tools/tillr-drain/drain/boundary.go` |
| waiting rule text and documents | view, drain and lander READMEs; `tillr-operating` skill |
