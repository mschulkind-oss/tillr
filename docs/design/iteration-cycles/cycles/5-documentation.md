# 5. Documentation

**Cycle ID**: `documentation`

**Purpose**: Create and refine documentation through iterative drafting, expert review, and editorial polish. Produces documentation that is accurate, complete, clear, and well-structured.

**When to use**: API documentation, user guides, architecture docs, onboarding materials, READMEs, or any work where the deliverable is *written prose about the system*.

### Roles

| Order | Role | Work Type | Inputs | Outputs | Authority |
|-------|------|-----------|--------|---------|-----------|
| 1 | **Researcher** | `doc-research` | Documentation target, codebase, existing docs | Information gathering report (code analysis, API surface, user needs) | Read-only investigation |
| 2 | **Drafter** | `doc-draft` | Research findings, documentation standards | Initial documentation draft | Content creation |
| 3 | **Reviewer** | `doc-review` | Draft, source code, accuracy requirements | Accuracy and completeness review with annotations | Advisory; flags issues |
| 4 | **Editor** | `doc-edit` | Reviewed draft, style guide | Refined documentation (language, structure, formatting) | Content modification |
| 5 | **Publisher** | `doc-publish` | Final draft | Integrated documentation (placed in correct location, linked, indexed) | File operations |

### Flow

```
┌──────────────────────────────────────────────────────────────────────┐
│                                                                      │
│  Researcher ──→ Drafter ──→ Reviewer ──→ Editor ──→ Publisher        │
│                    ▲            │                                     │
│                    │   issues   │                                     │
│                    └────────────┘                                     │
│                                                         │            │
│                                              approved   ▼            │
│                                                       DONE           │
└──────────────────────────────────────────────────────────────────────┘
```

**Step-by-step**:

1. **Researcher** gathers information needed for the documentation. Analyzes code, reads existing docs, identifies the audience, catalogs what needs to be covered. Output is a research document with organized findings.

2. **Drafter** writes the initial documentation based on research findings. Follows project documentation standards. Output is a complete first draft.

3. **Reviewer** checks the draft for accuracy (does it match the code?), completeness (does it cover everything?), and clarity (will the audience understand it?). Annotates issues. If significant inaccuracies exist, loops back to the drafter.

4. **Editor** refines language, improves structure, fixes formatting, ensures consistency with the project's documentation style. Output is a polished document.

5. **Publisher** places the document in the correct location within the project, adds it to any indexes or navigation, creates cross-links, and verifies it renders correctly.

### Quality Gate

| Condition | Threshold | Behavior on Fail |
|-----------|-----------|-------------------|
| Reviewer approval | No critical accuracy issues | Loop back to Drafter |
| Editor approval | Meets style and clarity standards | Loop back to Editor (self-revise) |

### Configuration

```json
{
  "cycle_id": "documentation",
  "max_iterations": 4,
  "quality_gate": {
    "type": "compound",
    "conditions": [
      { "type": "role_approval", "role": "doc-review" },
      { "type": "role_approval", "role": "doc-edit" }
    ]
  },
  "roles": ["doc-research", "doc-draft", "doc-review", "doc-edit", "doc-publish"],
  "typical_iterations": [2, 3],
  "on_max_iterations": "publish_with_disclaimer",
  "loop_target_on_fail": "doc-draft"
}
```

### Database Representation

```sql
-- Documentation work items
INSERT INTO work_items (feature_id, work_type, status, result)
VALUES ('feat-docs-1', 'doc-research', 'done',
        '{"topics": ["API endpoints", "authentication flow", "error codes"], "source_files": 12}');

INSERT INTO work_items (feature_id, work_type, status, result)
VALUES ('feat-docs-1', 'doc-draft', 'done',
        '{"file": "docs/api-reference.md", "word_count": 2400, "sections": 8}');

INSERT INTO work_items (feature_id, work_type, status, result)
VALUES ('feat-docs-1', 'doc-review', 'done',
        '{"accuracy_issues": 0, "completeness_gaps": 1, "clarity_issues": 3}');

INSERT INTO work_items (feature_id, work_type, status, result)
VALUES ('feat-docs-1', 'doc-edit', 'done',
        '{"changes": "Restructured auth section, added code examples, fixed formatting"}');

INSERT INTO work_items (feature_id, work_type, status, result)
VALUES ('feat-docs-1', 'doc-publish', 'done',
        '{"location": "docs/api-reference.md", "cross_links_added": 4}');
```

### JSON Schema

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "DocumentationCycle",
  "type": "object",
  "properties": {
    "cycle_id": { "const": "documentation" },
    "feature_id": { "type": "string" },
    "iteration": { "type": "integer", "minimum": 1 },
    "state": {
      "type": "string",
      "enum": ["doc-research", "doc-draft", "doc-review", "doc-edit", "doc-publish", "done", "escalated"]
    },
    "config": {
      "type": "object",
      "properties": {
        "max_iterations": { "type": "integer", "default": 4 },
        "style_guide": { "type": "string", "description": "Path to project style guide" }
      }
    },
    "document": {
      "type": "object",
      "properties": {
        "target_path": { "type": "string" },
        "audience": { "type": "string" },
        "type": { "type": "string", "enum": ["api-reference", "guide", "tutorial", "architecture", "readme"] }
      }
    }
  },
  "required": ["cycle_id", "feature_id", "iteration", "state"]
}
```

« [All cycles](./README.md) · [Iteration cycles overview](../README.md)
