# Open-Source Checklist — tillr

> Checklist for preparing tillr for public release.
> Follows the 11-section pattern from vantage/yolo_jail.

---

## What Goes Public vs. What Stays Private

### What Goes Public

| Path | Notes |
|------|-------|
| `src/` | Core source code |
| `cmd/` | CLI entry points |
| `internal/` | Internal packages |
| `web/` | Web frontend |
| `tests/` | Test suites |
| `go.mod` | Module definition |
| `go.sum` | Dependency checksums |
| `README.md` | Project overview |
| `LICENSE` | License file |
| `NOTICE` | Third-party attribution |
| `CONTRIBUTING.md` | Contribution guidelines |
| `CODE_OF_CONDUCT.md` | Community standards |
| `SECURITY.md` | Security policy |
| `.github/` | Actions, templates, dependabot |
| `docs/VISION.md` | Public vision document |
| `docs/design/` | Architecture and design docs |
| `docs/guides/` | User guides |

### What Stays Private

| Path | Why Private |
|------|-------------|
| `AGENTS.md` | Agent configuration, internal workflows |
| `OPEN_QUESTIONS.md` | Internal decision tracking |
| `.copilot/` | Copilot configuration |
| `.gemini/` | Gemini configuration |
| `.yolo/` | Yolo configuration |
| `yolo-jail.jsonc` | Yolo jail config |
| `docs/open-source-checklist.md` | This checklist (internal process) |
| `docs/NAME_CANDIDATES.md` | Naming brainstorm (internal) |
| `scratch/` | Scratch/experimental files |
| `trash/` | Deleted/archived files |
| `context/` | Internal context documents |

---

## 1. Name Decision

- [ ] Finalize project name (currently "tillr")
- [ ] Check PyPI availability for chosen name
- [ ] Check npm availability for chosen name
- [ ] Check crates.io availability for chosen name
- [ ] Check GitHub organization/repo availability
- [ ] Check Go module path availability (pkg.go.dev)
- [ ] Search for trademark conflicts
- [ ] Update all references if renaming (module path, imports, binary name, docs)

## 2. Legal & Licensing

- [ ] Choose license (MIT, Apache 2.0, or dual)
- [ ] Create `LICENSE` file
- [ ] Create `NOTICE` file for third-party attributions
- [ ] Audit all dependencies for license compatibility
- [ ] Decide on CLA (Contributor License Agreement) — yes/no
- [ ] If CLA: set up CLA bot (e.g., cla-assistant)
- [ ] Add license headers to source files (if required by chosen license)
- [ ] Verify no copyleft dependencies conflict with chosen license

## 3. Sensitive Content Scan

- [ ] Scan for hardcoded secrets (API keys, tokens, passwords)
- [ ] Scan for hardcoded paths (e.g., `/home/matt/`, `/Users/...`)
- [ ] Scan for personal information (email addresses, usernames, internal URLs)
- [ ] Scan for internal hostnames or IP addresses
- [ ] Scan for TODO/FIXME/HACK comments that reference internal systems
- [ ] Review git history for accidentally committed secrets
- [ ] Run `gitleaks detect` on the repository
- [ ] Verify `.gitignore` covers all sensitive file patterns

## 4. Repository Setup

- [ ] Set up jj bookmarks: `main`, `staging`, `dev`
- [ ] Configure public remote (GitHub public repo)
- [ ] Configure private remote (GitHub private repo)
- [ ] Create `public-files.txt` listing all files to push to public
- [ ] Verify push workflow: private → staging → public
- [ ] Test that private-only files are excluded from public pushes
- [ ] Set up branch protection rules on public repo (main)
- [ ] Configure default branch on public repo

## 5. Repository Hygiene

- [ ] Write `README.md` with project description, install instructions, usage examples
- [ ] Create `CONTRIBUTING.md` with development setup, PR process, code style
- [ ] Add `CODE_OF_CONDUCT.md` (Contributor Covenant or similar)
- [ ] Create `SECURITY.md` with vulnerability reporting instructions
- [ ] Set up GitHub issue templates (bug report, feature request)
- [ ] Set up GitHub PR template
- [ ] Add `.editorconfig` for consistent formatting
- [ ] Create `CHANGELOG.md` or decide on release notes strategy

## 6. CI/CD

- [ ] Set up GitHub Actions workflow for CI (build + test)
- [ ] Set up GitHub Actions workflow for linting
- [ ] Add CI status badge to README
- [ ] Configure Dependabot for dependency updates
- [ ] Set up `gitleaks` in CI for secret scanning
- [ ] Set up release automation (GoReleaser or similar)
- [ ] Configure branch protection to require CI passing
- [ ] Test CI runs on a sample PR

## 7. Build & Install Verification

- [ ] Test `git clone` + build from scratch on clean machine
- [ ] Verify `go install github.com/<org>/<name>@latest` works
- [ ] Verify binary runs and shows help output
- [ ] Test on Linux (primary target)
- [ ] Test on macOS
- [ ] Verify all build tags and conditional compilation work
- [ ] Test with minimum supported Go version
- [ ] Verify no CGO dependencies (or document them)

## 8. Code Readiness

- [ ] Implement `doctor` or `health` command for self-diagnostics
- [ ] Replace any hardcoded defaults with sensible generic defaults
- [ ] Ensure all error messages are user-friendly (no raw stack traces)
- [ ] Add `--version` flag with build info
- [ ] Verify all CLI commands have help text
- [ ] Remove or gate any debug/development-only commands
- [ ] Ensure graceful degradation when optional dependencies are missing
- [ ] Run `go vet`, `staticcheck`, and `golangci-lint`

## 9. Documentation

- [ ] Write quick-start guide (docs/guides/quick-start.md)
- [ ] Write user guide covering core workflows
- [ ] Document architecture (docs/design/architecture.md)
- [ ] Document configuration file format and options
- [ ] Add usage examples for common workflows
- [ ] Document environment variables
- [ ] Create FAQ or troubleshooting guide
- [ ] Review all docs for internal references or assumptions

## 10. Branding & Presentation

- [ ] Write compelling repo description (one-liner)
- [ ] Add relevant GitHub topics/tags
- [ ] Create social preview image (1280×640)
- [ ] Prepare initial release (v0.1.0) with release notes
- [ ] Create demo GIF or screenshot for README
- [ ] Write a clear "Why this tool?" section in README
- [ ] Ensure repo has a clean, professional first impression

## 11. Post-Launch

- [ ] Monitor issues and discussions for first 2 weeks
- [ ] Respond to initial bug reports promptly
- [ ] Announce on Reddit (r/golang, r/commandline, r/programming)
- [ ] Submit to Hacker News
- [ ] Post in relevant Discord/Slack communities
- [ ] Share on social media (Twitter/X, Mastodon, LinkedIn)
- [ ] Add to awesome-go or similar curated lists
- [ ] Write a blog post or announcement explaining the project
- [ ] Set up GitHub Discussions if community engagement grows
- [ ] Plan v0.2.0 based on initial feedback
