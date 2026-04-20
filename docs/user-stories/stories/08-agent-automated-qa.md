# 8. Agent — Performing Automated QA

**Context:** After an implementing agent submits a feature, a QA agent can
run automated checks before the feature reaches human QA. This is the
`agent-qa` phase.

**What the QA agent does:**

1. Claims a feature in `agent-qa` status:
   ```
   tillr feature list --status agent-qa --json
   ```

2. For each feature, runs automated checks based on the feature type:
   - **UI features:** Takes screenshots, checks responsive layouts, verifies
     accessibility basics.
   - **API features:** Runs the test suite, checks endpoint responses match
     the spec.
   - **CLI features:** Runs the command with various inputs, verifies output
     format.
   - **All features:** Runs the build, runs the linter, checks for
     regressions.

3. Records results:
   ```
   tillr qa submit login-page --passed --notes "All checks pass. Screenshots attached."
   ```
   or
   ```
   tillr qa submit login-page --failed --notes "Login form doesn't handle empty email field."
   ```

4. On pass, the feature advances to `human-qa`. On fail, it goes back to
   `implementing` with the failure notes attached.

**Gap: What guides the QA agent's testing?**

Today there's no structured test plan in the feature. The QA agent has to
infer what to test from the spec. For consistent quality, each feature
should have a `test_plan` field — distinct from the `spec` — that tells
both agent and human QA what to check.

The test plan should have two sections:
- **Agent checks:** Things that can be automated. "Build succeeds."
  "Tests pass." "Endpoint returns 200." "Screenshot matches baseline."
- **Human checks:** Things only a human can judge. "Login page looks right."
  "Error messages are clear." "Flow feels natural."

The agent runs the agent checks. The human reviews the human checks. Neither
wastes time on the other's domain.

**Gap: How does the QA agent know what TYPE of feature it is?**

Features don't have a `type` field today (ui, api, cli, migration, etc.).
The QA agent has to guess from the name and spec. Adding a type would let
the QA agent pick the right test strategy automatically.

---

« [All stories](./README.md) · [User-stories overview](../README.md)
