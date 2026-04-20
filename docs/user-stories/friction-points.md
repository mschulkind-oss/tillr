# Friction Points → Feature Gaps

Things that came up across stories. These map to roadmap work.

| # | Friction Point | Who hits it | Status |
|---|---------------|-------------|--------|
| 1 | Empty state in UI gives no guidance | [Sam](./stories/01-sam-solo-developer.md), [Marcus](./stories/04-marcus-onboarding.md) | Missing |
| 2 | No guided "connect your agent" flow | [Sam](./stories/01-sam-solo-developer.md), [Kenji](./stories/02-kenji-tech-lead.md) | Missing |
| 3 | QA is a status, not an experience (no test plan, no guided review) | [Sam](./stories/01-sam-solo-developer.md), [Lisa](./stories/03-lisa-product-owner.md), [Val](./stories/09-val-global-inbox.md) | Missing — `human-qa-experience-design` in draft |
| 4 | No global inbox across workstreams | [Lisa](./stories/03-lisa-product-owner.md), [Val](./stories/09-val-global-inbox.md) | Missing |
| 5 | Features can enter queue without specs | [Val](./stories/09-val-global-inbox.md), [Agent](./stories/06-agent-claiming-from-queue.md) | Missing — no validation |
| 6 | Test plan field (separate from spec) with agent vs human sections | [Agent QA](./stories/08-agent-automated-qa.md), [Lisa](./stories/03-lisa-product-owner.md) | Missing |
| 7 | UI is read-only for most mutations (can't add features, reprioritize) | [Lisa](./stories/03-lisa-product-owner.md), [Marcus](./stories/04-marcus-onboarding.md) | Partial — some inline editing exists |
| 8 | No notifications (email, Slack, browser) | [Lisa](./stories/03-lisa-product-owner.md) | Missing |
| 9 | `tillr daemon add <path>` command | [Rachel](./stories/05-rachel-daemon-multi-project.md), [Derek](./stories/10-derek-second-project.md) | Missing |
| 10 | Daemon auto-reload config on change | [Rachel](./stories/05-rachel-daemon-multi-project.md) | Missing |
| 11 | Cross-project global inbox in daemon mode | [Rachel](./stories/05-rachel-daemon-multi-project.md), [Derek](./stories/10-derek-second-project.md) | Missing |
| 12 | `tillr serve` → daemon upgrade path | [Derek](./stories/10-derek-second-project.md) | Missing |
| 13 | Service installation for daemon (systemd/launchd) | [Derek](./stories/10-derek-second-project.md) | Missing |
| 14 | Conflict detection between concurrent agents | [Kenji](./stories/02-kenji-tech-lead.md) | Missing |
| 15 | SQLite busy timeout for concurrent agent access | [Kenji](./stories/02-kenji-tech-lead.md) | Unverified |
| 16 | `tillr agent submit` degrades without `gh` | [Agent in jail](./stories/07-agent-in-worktree-jail.md) | Missing |
| 17 | Feature type field (ui, api, cli, migration) | [Agent QA](./stories/08-agent-automated-qa.md) | Missing |
| 18 | Richer claim response (includes spec, context, workstream) | [Agent](./stories/06-agent-claiming-from-queue.md) | Partial |
| 19 | `tillr onboard --yes` non-interactive mode | [Kenji](./stories/02-kenji-tech-lead.md) | Exists but may need polish |
| 20 | Export/import for portable history (DB travels with repo) | [Marcus](./stories/04-marcus-onboarding.md) | `export-git` exists, not in workflow |
| 21 | Human-readable status labels in UI ("Ready for review" not "human-qa") | [Lisa](./stories/03-lisa-product-owner.md) | Missing |
| 22 | Spec-required validation before features are claimable | [Val](./stories/09-val-global-inbox.md), [Agent](./stories/06-agent-claiming-from-queue.md) | Missing |
| 23 | Inline approve/reject in global inbox with summary | [Val](./stories/09-val-global-inbox.md), [Lisa](./stories/03-lisa-product-owner.md) | Missing |

---

« [User-stories overview](./README.md) · [Key insight](./key-insight.md) · [All stories](./stories/README.md)
