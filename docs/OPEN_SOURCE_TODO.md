# Open-Sourcing Tillr — Human Todo List

Everything below is **your** action list. Agent-side prep (CI workflows, README polish, file classification, Justfile promote workflow, .gitignore, CHANGELOG, Committing-Changes skill) is already done and committed.

---

## 1. Create the Public GitHub Repository

```bash
gh repo create mschulkind/tillr --public \
  --description "Human-in-the-loop project management for agentic software development"
```

Then add it as the `public` remote in jj:

```bash
jj git remote add public git@github.com:mschulkind/tillr.git
```

> **Note:** The current `private` remote points to `git@github.com:mschulkind/tillr.git` — you may want a separate private repo name (e.g., `tillr-private`) or just use the same repo and control access via branch protection. Decide before pushing.

---

## 2. Decide on Repository Name

The Go module path is currently `github.com/mschulkind/tillr`. If you want a different org or name:

- [ ] Check availability: `gh api repos/{owner}/{name}` (404 = available)
- [ ] Update `go.mod` module path
- [ ] Update all import paths in `internal/`, `cmd/`
- [ ] Update README badges and URLs
- [ ] Update CI workflow badge URLs

If keeping `mschulkind/tillr`, skip this step.

---

## 3. Review Security Contact Emails

These files contain `mschulkind@gmail.com`:

- **SECURITY.md** line 7 — security vulnerability reports go here
- **CODE_OF_CONDUCT.md** line 48 — conduct enforcement contact

Options:
- [ ] Keep personal email (simplest)
- [ ] Create a project email (e.g., `tillr-security@...`)
- [ ] Use GitHub Security Advisories instead (recommended for SECURITY.md)

---

## 4. First Public Push

Once the public remote is set up:

```bash
# Ensure staging has your release description
jj describe -r staging -m "feat: initial public release — CLI, web viewer, iteration cycles"

# Promote staging → main
just promote
```

This pushes `main` to both public and private remotes.

---

## 5. Configure GitHub Repository Settings

On the public repo (github.com/mschulkind/tillr):

- [ ] **Description**: "Human-in-the-loop project management for agentic software development"
- [ ] **Topics**: `project-management`, `ai-agents`, `cli`, `go`, `sqlite`, `developer-tools`
- [ ] **Website**: leave blank (or link to docs)
- [ ] **Branch protection** on `main`: require CI to pass, require PR reviews
- [ ] **Enable Issues** (should be default)
- [ ] **Enable Discussions** (optional — good for community Q&A)
- [ ] **Social preview image** (optional — a screenshot of the web dashboard)

---

## 6. Verify CI Runs

After the first push:

- [ ] Check that the CI workflow runs and passes on `main`
- [ ] Manually trigger a test PR to verify PR checks work

---

## 7. Tag First Release

```bash
# Create and push a version tag
jj git push --bookmark main --remote public
git tag v0.1.0-alpha
git push public v0.1.0-alpha
```

This triggers the Release workflow which builds cross-platform binaries and creates a GitHub Release.

---

## 8. Verify Clean Install

From a fresh machine or container:

```bash
go install github.com/mschulkind/tillr/cmd/tillr@latest
tillr --version
tillr init test-project
tillr doctor
tillr serve
```

Ensure everything works without any files from your dev environment.

---

## 9. Multi-Project Path

Tillr already supports multiple projects:

- Each project has its own `.tillr.json` and `tillr.db` in the project root
- `go install` puts a single binary in `$GOPATH/bin`
- Running `tillr init my-project` in any directory sets up that directory
- The `active_project` config key supports project switching

**Future enhancements** (roadmap, not blocking):
- `tillr project list` — show all known projects
- `tillr project select <name>` — switch active project
- Global config at `~/.tillr.yaml` for cross-project defaults

---

## 10. Post-Launch

- [ ] Monitor GitHub Issues for first-time user friction
- [ ] Write a blog post or announcement (optional)
- [ ] Consider adding to [awesome-go](https://github.com/avelino/awesome-go) list
- [ ] Set up GitHub Sponsors (optional)

---

## Summary of Agent-Completed Prep

| Task | Status |
|------|--------|
| Public file classification (`scripts/public-files.txt`) | ✅ Done |
| `.gitignore` updated for private files | ✅ Done |
| CI workflow updated (includes frontend build) | ✅ Done |
| Release workflow updated (frontend artifact) | ✅ Done |
| Justfile push/prepromote/promote recipes | ✅ Done |
| README polished for public audience | ✅ Done |
| CHANGELOG.md created | ✅ Done |
| Committing-Changes skill updated | ✅ Done |
| FeatureDetail API response fix | ✅ Done |
| Live-reload dev workflow | ✅ Done |
