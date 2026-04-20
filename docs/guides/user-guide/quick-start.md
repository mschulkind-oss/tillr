# Quick Start

Get a project running in two minutes:

```bash
# 1. Initialize a new project
tillr init my-app

# 2. Add a milestone and a feature
tillr milestone add "v1.0"
tillr feature add "User authentication" --milestone v1.0 --priority high

# 3. Start a cycle and hand work to an agent
tillr cycle start implement feat-1
tillr next          # Returns JSON with agentPrompt

# 4. Agent completes work; mark it done
tillr done --result "Implemented JWT auth with refresh tokens"

# 5. Review the result
tillr qa approve feat-1 --notes "Looks good, tests pass"

# 6. See everything in the web viewer
tillr serve
# Open http://localhost:3847
```

---

« [User Guide](./README.md)
