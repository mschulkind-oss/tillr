package orchestrator

// PersonaJSONSchema is the structured output every persona MUST emit
// per `claude -p --json-schema`. The orchestrator passes this as the
// schema; claude validates the agent's final response against it.
//
// This is the structural enforcement point (Principle Zero). The
// agent doesn't have to remember to "summarize what you did" or
// "list follow-ups" — the schema requires it. If the agent emits
// non-conformant JSON, claude's structured-output validator rejects.
const PersonaJSONSchema = `{
  "type": "object",
  "required": ["summary", "result"],
  "additionalProperties": false,
  "properties": {
    "summary": {
      "type": "string",
      "description": "One-line summary of what was done. Visible to the human PM."
    },
    "context_entry": {
      "type": "string",
      "description": "Markdown body to append to your persona context file. Include reusable knowledge: patterns established, gotchas hit, project conventions discovered. Empty string if nothing reusable."
    },
    "files_touched": {
      "type": "array",
      "items": {"type": "string"},
      "description": "Project-relative paths of files modified or created."
    },
    "follow_up_features": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["persona", "title"],
        "additionalProperties": false,
        "properties": {
          "persona": {"type": "string", "description": "Target persona for the new feature."},
          "title":   {"type": "string"},
          "description": {"type": "string"}
        }
      },
      "description": "New features to file based on what was learned during this run (e.g., bugs spotted in adjacent code, research questions that arose)."
    },
    "result": {
      "type": "string",
      "enum": ["completed", "blocked", "needs_review"],
      "description": "completed = fully done, ready for next stage. blocked = cannot proceed without human/PM input. needs_review = done but the human should look closely (e.g., made a non-obvious decision)."
    },
    "blocker": {
      "type": "string",
      "description": "If result is 'blocked', what is blocking. Empty otherwise."
    }
  }
}`

// Result is the parsed structured output a persona emits.
type Result struct {
	Summary          string            `json:"summary"`
	ContextEntry     string            `json:"context_entry"`
	FilesTouched     []string          `json:"files_touched"`
	FollowUpFeatures []FollowUpFeature `json:"follow_up_features"`
	Result           string            `json:"result"` // completed | blocked | needs_review
	Blocker          string            `json:"blocker"`
}

// FollowUpFeature is one entry in the follow_up_features array.
type FollowUpFeature struct {
	Persona     string `json:"persona"`
	Title       string `json:"title"`
	Description string `json:"description"`
}
