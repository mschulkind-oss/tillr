# 7. Release

**Cycle ID**: `release`

**Purpose**: Prepare and ship a release with quality confidence. This cycle is linear — it doesn't loop back to the beginning, but individual stages can retry on failure.

**When to use**: Cutting a release, shipping a version, publishing a package, or any workflow that takes existing work and packages it for delivery.

### Roles

| Order | Role | Work Type | Inputs | Outputs | Authority |
|-------|------|-----------|--------|---------|-----------|
| 1 | **Freezer** | `release-freeze` | Branch state, feature list | Release branch, changelog draft, inclusion list | Branch management |
| 2 | **QA** | `release-qa` | Release branch | Comprehensive test results (unit, integration, manual) | Testing only |
| 3 | **Fixer** | `release-fix` | Failed tests, QA report | Targeted fixes on the release branch | Code changes (release branch only) |
| 4 | **Stager** | `release-stage` | Tested release branch | Staging deployment, deployment verification | Deployment |
| 5 | **Verifier** | `release-verify` | Staging environment | Final validation report (smoke tests, sanity checks) | Testing only |
| 6 | **Shipper** | `release-ship` | Verified staging | Tagged release, published changelog, distribution | Release publication |

### Flow

```
┌───────────────────────────────────────────────────────────────────────────────┐
│                                                                               │
│  Freezer ──→ QA ──→ Fixer ──→ Stager ──→ Verifier ──→ Shipper ──→ DONE       │
│                │       ▲         │           ▲                                │
│                │ fail  │         │   fail    │                                │
│                └───────┘         └───────────┘                                │
└───────────────────────────────────────────────────────────────────────────────┘
```

**Step-by-step**:

1. **Freezer** creates the release branch from the current stable state. Documents what's included (features, fixes, changes). Drafts the changelog. Output is a release branch and inclusion manifest.

2. **QA** runs comprehensive testing against the release branch: unit tests, integration tests, manual smoke tests. Output is a detailed test report with pass/fail for each category.

3. **Fixer** addresses any failures found by QA. Fixes are applied directly to the release branch and are minimal — only what's needed to fix the issue. After fixing, QA re-runs (loops between QA and Fixer until clean).

4. **Stager** deploys the release branch to a staging environment. Verifies the deployment succeeded. Output is a deployment confirmation with environment details.

5. **Verifier** performs final validation on staging. Smoke tests key user flows, checks for deployment-specific issues (environment variables, configurations, external service connectivity). If issues are found, loops back to Stager or Fixer.

6. **Shipper** tags the release in version control, finalizes the changelog, publishes to distribution channels (package registry, GitHub release, etc.), and announces the release.

### Quality Gate

| Condition | Threshold | Behavior on Fail |
|-----------|-----------|-------------------|
| All tests pass | 100% pass rate | Loop between QA and Fixer |
| Staging verification | Smoke tests pass | Loop between Stager and Verifier |
| Changelog updated | Required | Block Shipper until complete |

### Configuration

```json
{
  "cycle_id": "release",
  "max_iterations": 5,
  "quality_gate": {
    "type": "compound",
    "conditions": [
      { "type": "all_tests_pass" },
      { "type": "staging_verified" },
      { "type": "changelog_updated" }
    ]
  },
  "roles": ["release-freeze", "release-qa", "release-fix", "release-stage", "release-verify", "release-ship"],
  "typical_iterations": [1, 3],
  "on_max_iterations": "abort_release",
  "allow_hotfix_loop": true
}
```

### Database Representation

```sql
-- Release tracked as a feature with the release cycle
INSERT INTO features (id, project_id, name, status, assigned_cycle)
VALUES ('release-v0.2.0', 'proj-1', 'Release v0.2.0', 'implementing', 'release');

-- Work items track each stage
INSERT INTO work_items (feature_id, work_type, status, result)
VALUES ('release-v0.2.0', 'release-freeze', 'done',
        '{"branch": "release/v0.2.0", "features_included": 5, "fixes_included": 3}');

INSERT INTO work_items (feature_id, work_type, status, result)
VALUES ('release-v0.2.0', 'release-qa', 'done',
        '{"unit_tests": {"passed": 142, "failed": 0}, "integration_tests": {"passed": 38, "failed": 1}}');

INSERT INTO work_items (feature_id, work_type, status, result)
VALUES ('release-v0.2.0', 'release-ship', 'done',
        '{"tag": "v0.2.0", "changelog": "CHANGELOG.md", "published_to": ["github-releases"]}');

-- Release event
INSERT INTO events (project_id, feature_id, event_type, data)
VALUES ('proj-1', 'release-v0.2.0', 'release-shipped', '{"version": "v0.2.0", "date": "2025-01-15"}');
```

### JSON Schema

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "ReleaseCycle",
  "type": "object",
  "properties": {
    "cycle_id": { "const": "release" },
    "feature_id": { "type": "string" },
    "iteration": { "type": "integer", "minimum": 1 },
    "state": {
      "type": "string",
      "enum": ["release-freeze", "release-qa", "release-fix", "release-stage", "release-verify", "release-ship", "done", "aborted"]
    },
    "config": {
      "type": "object",
      "properties": {
        "max_iterations": { "type": "integer", "default": 5 },
        "allow_hotfix_loop": { "type": "boolean", "default": true },
        "staging_environment": { "type": "string" }
      }
    },
    "release": {
      "type": "object",
      "properties": {
        "version": { "type": "string" },
        "branch": { "type": "string" },
        "features_included": { "type": "array", "items": { "type": "string" } },
        "tag": { "type": "string" }
      }
    }
  },
  "required": ["cycle_id", "feature_id", "iteration", "state"]
}
```

« [All cycles](./README.md) · [Iteration cycles overview](../README.md)
