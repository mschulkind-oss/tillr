package mcp

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"

	"github.com/mschulkind/lifecycle/internal/db"
	"github.com/mschulkind/lifecycle/internal/engine"
	"github.com/mschulkind/lifecycle/internal/models"
)

// Server implements a minimal MCP (Model Context Protocol) server over stdio.
// It exposes lifecycle tools via JSON-RPC 2.0 so AI agents can interact
// with the project without subprocess CLI calls.
type Server struct {
	db        *sql.DB
	projectID string
	in        io.Reader
	out       io.Writer
}

// Request is a JSON-RPC 2.0 request.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is a JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *RPCError `json:"error,omitempty"`
}

// RPCError is a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Tool describes an MCP tool exposed by the server.
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

// Content is an MCP text content block returned from tool calls.
type Content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// NewServer creates an MCP server backed by the given database and project.
func NewServer(database *sql.DB, projectID string, in io.Reader, out io.Writer) *Server {
	return &Server{db: database, projectID: projectID, in: in, out: out}
}

// Run reads line-delimited JSON-RPC requests from stdin and writes responses to stdout.
func (s *Server) Run() error {
	reader := bufio.NewReader(s.in)
	for {
		line, err := reader.ReadBytes('\n')
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading stdin: %w", err)
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			continue // skip malformed lines
		}

		// Notifications have no ID and expect no response.
		if req.ID == nil {
			continue
		}

		resp := s.HandleRequest(req)
		out, _ := json.Marshal(resp)
		_, _ = fmt.Fprintf(s.out, "%s\n", out)
	}
}

// HandleRequest dispatches a JSON-RPC request to the appropriate handler.
func (s *Server) HandleRequest(req Request) Response {
	switch req.Method {
	case "initialize":
		return Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "lifecycle", "version": "0.1.0"},
			},
		}
	case "tools/list":
		return Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]any{"tools": s.listTools()},
		}
	case "tools/call":
		return s.handleToolCall(req)
	default:
		return Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &RPCError{Code: -32601, Message: "method not found"},
		}
	}
}

func (s *Server) listTools() []Tool {
	return []Tool{
		{
			Name:        "lifecycle_next",
			Description: "Get the next work item for an agent to work on. Returns full context including feature spec, cycle state, and prior results.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent_id": map[string]string{"type": "string", "description": "Agent identifier for work item assignment"},
				},
			},
		},
		{
			Name:        "lifecycle_done",
			Description: "Mark the current work item as complete with a result summary.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"result": map[string]string{"type": "string", "description": "Result description of the completed work"},
				},
			},
		},
		{
			Name:        "lifecycle_fail",
			Description: "Mark the current work item as failed with a reason.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"reason": map[string]string{"type": "string", "description": "Failure reason"},
				},
			},
		},
		{
			Name:        "lifecycle_status",
			Description: "Get project status overview including feature counts, milestones, active cycles, and recent events.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "lifecycle_features",
			Description: "List all features with optional status filter.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"status": map[string]string{"type": "string", "description": "Filter by status (draft, planning, implementing, agent-qa, human-qa, done, blocked)"},
				},
			},
		},
		{
			Name:        "lifecycle_feedback",
			Description: "Submit feedback, feature ideas, or bug reports to the project idea queue.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":       map[string]string{"type": "string", "description": "Feedback title"},
					"description": map[string]string{"type": "string", "description": "Detailed description"},
					"type":        map[string]string{"type": "string", "description": "Type: feature, bug, or feedback"},
				},
				"required": []string{"title"},
			},
		},
	}
}

func (s *Server) handleToolCall(req Request) Response {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &RPCError{Code: -32602, Message: "invalid params"},
		}
	}

	result, err := s.dispatchTool(params.Name, params.Arguments)
	if err != nil {
		return Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"content": []Content{{Type: "text", Text: fmt.Sprintf("error: %s", err.Error())}},
				"isError": true,
			},
		}
	}

	return Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"content": []Content{{Type: "text", Text: result}},
		},
	}
}

func (s *Server) dispatchTool(name string, args json.RawMessage) (string, error) {
	switch name {
	case "lifecycle_next":
		return s.toolNext(args)
	case "lifecycle_done":
		return s.toolDone(args)
	case "lifecycle_fail":
		return s.toolFail(args)
	case "lifecycle_status":
		return s.toolStatus()
	case "lifecycle_features":
		return s.toolFeatures(args)
	case "lifecycle_feedback":
		return s.toolFeedback(args)
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

func (s *Server) toolNext(args json.RawMessage) (string, error) {
	var params struct {
		AgentID string `json:"agent_id"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &params)
	}

	var w *models.WorkItem
	var err error
	if params.AgentID != "" {
		w, err = engine.GetNextWorkItem(s.db, params.AgentID)
	} else {
		w, err = engine.GetNextWorkItem(s.db)
	}
	if err != nil {
		return "", fmt.Errorf("getting next work item: %w", err)
	}

	ctx, err := engine.GetWorkContext(s.db, w)
	if err != nil {
		return "", fmt.Errorf("getting work context: %w", err)
	}

	return toJSON(ctx)
}

func (s *Server) toolDone(args json.RawMessage) (string, error) {
	var params struct {
		Result string `json:"result"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &params)
	}

	if err := engine.CompleteWorkItem(s.db, params.Result); err != nil {
		return "", fmt.Errorf("completing work item: %w", err)
	}
	return `{"status":"completed"}`, nil
}

func (s *Server) toolFail(args json.RawMessage) (string, error) {
	var params struct {
		Reason string `json:"reason"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &params)
	}

	if err := engine.FailWorkItem(s.db, params.Reason); err != nil {
		return "", fmt.Errorf("failing work item: %w", err)
	}
	return `{"status":"failed"}`, nil
}

func (s *Server) toolStatus() (string, error) {
	overview, err := engine.GetStatusOverview(s.db)
	if err != nil {
		return "", fmt.Errorf("getting status: %w", err)
	}
	return toJSON(overview)
}

func (s *Server) toolFeatures(args json.RawMessage) (string, error) {
	var params struct {
		Status string `json:"status"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &params)
	}

	features, err := db.ListFeatures(s.db, s.projectID, params.Status, "")
	if err != nil {
		return "", fmt.Errorf("listing features: %w", err)
	}
	return toJSON(features)
}

func (s *Server) toolFeedback(args json.RawMessage) (string, error) {
	var params struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Type        string `json:"type"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &params); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
	}
	if params.Title == "" {
		return "", fmt.Errorf("title is required")
	}
	if params.Type == "" {
		params.Type = "feedback"
	}

	idea := &models.IdeaQueueItem{
		ProjectID:   s.projectID,
		Title:       params.Title,
		RawInput:    params.Description,
		IdeaType:    params.Type,
		Status:      "pending",
		SubmittedBy: "mcp",
	}
	if err := db.InsertIdea(s.db, idea); err != nil {
		return "", fmt.Errorf("creating feedback: %w", err)
	}
	return toJSON(idea)
}

func toJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("marshaling JSON: %w", err)
	}
	return string(b), nil
}
