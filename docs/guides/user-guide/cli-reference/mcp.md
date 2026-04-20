# MCP Server

Start a Model Context Protocol (MCP) server for direct agent integration over stdio.

```bash
tillr mcp
```

The MCP server exposes tillr tools (`tillr_next`, `tillr_done`, `tillr_fail`, `tillr_status`, `tillr_features`, `tillr_feedback`) via JSON-RPC 2.0 over stdin/stdout. This allows AI agents to interact with tillr directly without subprocess CLI calls.

---

« [CLI Reference](./README.md) · [User Guide](../README.md)
