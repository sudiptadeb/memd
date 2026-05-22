// Package mcp implements a minimal MCP (Model Context Protocol) server
// over HTTP. It supports initialize, tools/list, and tools/call — enough
// for clients like Claude Code and Codex CLI to discover and invoke the
// five memory_* tools that this server exposes.
package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/sudiptadeb/memd/server/internal/logs"
	"github.com/sudiptadeb/memd/server/internal/registry"
	"github.com/sudiptadeb/memd/server/internal/storage"
)

const protocolVersion = "2025-03-26"

// Server is an MCP endpoint backed by a registry.
type Server struct {
	reg          *registry.Registry
	instructions string
	serverName   string
	serverVer    string
}

func New(reg *registry.Registry, instructions, name, version string) *Server {
	return &Server{
		reg:          reg,
		instructions: instructions,
		serverName:   name,
		serverVer:    version,
	}
}

// Mount registers the MCP handler under prefix. Each request must come in at
// prefix + "<token>" where token resolves to a connector.
func (s *Server) Mount(mux *http.ServeMux, prefix string) {
	mux.HandleFunc(prefix, s.handle)
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	tok := strings.TrimPrefix(r.URL.Path, "/mcp/")
	tok = strings.Trim(tok, "/")
	if tok == "" || strings.Contains(tok, "/") {
		http.NotFound(w, r)
		return
	}
	conn := s.reg.ConnectorByToken(tok)
	if conn == nil {
		http.NotFound(w, r)
		return
	}

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req rpcReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeRPCError(w, nil, -32700, "parse error: "+err.Error())
		return
	}

	resp := s.dispatch(conn, &req)
	if resp == nil {
		// JSON-RPC notification — MCP Streamable HTTP spec requires
		// 202 Accepted with no body. Strict clients (rmcp / Codex CLI)
		// treat any other status as a protocol error and close the
		// transport.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// --- JSON-RPC envelope ---

type rpcReq struct {
	Jsonrpc string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
}

type rpcResp struct {
	Jsonrpc string          `json:"jsonrpc"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
	ID      json.RawMessage `json:"id"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rpcResp{
		Jsonrpc: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: msg},
	})
}

// --- Dispatch ---

func (s *Server) dispatch(conn *registry.Connector, req *rpcReq) *rpcResp {
	switch req.Method {
	case "initialize":
		logs.Info("MCP initialize from connector %q", conn.Name)
		return s.handleInitialize(conn, req)
	case "notifications/initialized":
		return nil
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(conn, req)
	case "ping":
		return &rpcResp{Jsonrpc: "2.0", ID: req.ID, Result: map[string]any{}}
	default:
		return &rpcResp{
			Jsonrpc: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32601, Message: "method not found: " + req.Method},
		}
	}
}

func (s *Server) handleInitialize(_ *registry.Connector, req *rpcReq) *rpcResp {
	// The instructions field carries the doctrine only (stable, small).
	// Active memory content arrives via the memory_load tool, which the
	// doctrine instructs the agent to call as its first action.
	return &rpcResp{
		Jsonrpc: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    s.serverName,
				"version": s.serverVer,
			},
			"instructions": s.instructions,
		},
	}
}

// activeMemorySection composes a snapshot of the connector's accessible
// directories. For each directory it renders:
//
//   - the directory's metadata (id, backend, purpose),
//   - a shallow topology — root entries plus the direct children of memory/
//     if present — so the agent can see where things live without paying for
//     a recursive listing,
//   - the full contents of MEMORY.md (the canonical entry page).
//
// For deeper navigation the agent uses memory_list / memory_read.
func (s *Server) activeMemorySection(conn *registry.Connector) string {
	dirs := s.reg.DirectoriesForConnector(conn)
	var sb strings.Builder
	sb.WriteString("# Active Memory\n\n")
	if len(dirs) == 0 {
		sb.WriteString("_No directories are accessible through this connector._\n")
		return sb.String()
	}
	sb.WriteString("Regenerated on every `memory_load` call — the current state of memory. Treat the contents below as memory you already know. For deeper navigation, call `memory_list` on a folder or `memory_read` on a specific page.\n\n")
	for _, d := range dirs {
		fmt.Fprintf(&sb, "### %s\n\n", d.Directory.Name)
		fmt.Fprintf(&sb, "- id: `%s`\n", d.Directory.ID)
		fmt.Fprintf(&sb, "- backend: %s\n", d.Directory.Backend)
		if d.Directory.Description != "" {
			fmt.Fprintf(&sb, "- purpose: %s\n", d.Directory.Description)
		}
		sb.WriteString("\n")

		root, err := d.Backend.ListPath("")
		if err != nil {
			fmt.Fprintf(&sb, "_(could not list directory: %v)_\n\n", err)
			continue
		}
		sb.WriteString("**Topology (root + first layer of `memory/`):**\n\n```\n")
		writeTopology(&sb, d.Backend, root)
		sb.WriteString("```\n\n")

		body, err := d.Backend.Read("MEMORY.md")
		if err != nil {
			sb.WriteString("_(MEMORY.md missing — bootstrap with `memory_write`)_\n\n")
			continue
		}
		sb.WriteString("**`MEMORY.md`:**\n\n```markdown\n")
		sb.Write(body)
		if len(body) == 0 || body[len(body)-1] != '\n' {
			sb.WriteString("\n")
		}
		sb.WriteString("```\n\n")
	}
	return sb.String()
}

// writeTopology renders root entries. For a folder named "memory" we expand
// one level deeper (its direct children); for any other folder we just show
// the name with a child count.
func writeTopology(sb *strings.Builder, b storage.Backend, root []storage.DirEntry) {
	for _, e := range root {
		if !e.IsDir {
			fmt.Fprintf(sb, "%s\n", e.Name)
			continue
		}
		children, err := b.ListPath(e.Path)
		if err != nil {
			fmt.Fprintf(sb, "%s/  (could not list)\n", e.Name)
			continue
		}
		if e.Name == "memory" {
			fmt.Fprintf(sb, "%s/\n", e.Name)
			for _, c := range children {
				if c.IsDir {
					deep, _ := b.ListPath(c.Path)
					fmt.Fprintf(sb, "  %s/  (%d items)\n", c.Name, len(deep))
				} else {
					fmt.Fprintf(sb, "  %s\n", c.Name)
				}
			}
		} else {
			fmt.Fprintf(sb, "%s/  (%d items)\n", e.Name, len(children))
		}
	}
}

// --- Tool catalog ---

var toolsCatalog = []map[string]any{
	{
		"name":        "memory_load",
		"description": "MUST be called once at the start of every conversation, before responding to anything else. Returns your active memory — every accessible directory's description, page listing, and the full contents of its top-level MEMORY.md. Treat its result as memory you already know.",
		"inputSchema": map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	},
	{
		"name":        "memory_directories",
		"description": "List the memory directories this connector can access (no content). Rarely needed — memory_load returns more.",
		"inputSchema": map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	},
	{
		"name":        "memory_search",
		"description": "Search memory pages for a query. Returns matching lines with file paths.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":        map[string]any{"type": "string", "description": "Text to search for (case-insensitive)."},
				"directory_id": map[string]any{"type": "string", "description": "Restrict the search to one directory. If omitted, all visible directories are searched."},
				"limit":        map[string]any{"type": "integer", "description": "Maximum number of hits. Default 50."},
			},
			"required": []string{"query"},
		},
	},
	{
		"name":        "memory_read",
		"description": "Read one memory page in full.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"directory_id": map[string]any{"type": "string"},
				"path":         map[string]any{"type": "string", "description": "Page path relative to the directory root (e.g. 'MEMORY.md' or 'memory/feedback/foo.md')."},
			},
			"required": []string{"directory_id", "path"},
		},
	},
	{
		"name":        "memory_list",
		"description": "List the direct children of a path inside a memory directory. Use to dive into a folder the Active Memory topology shows by name. Pass an empty path (or omit it) to list the directory root.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"directory_id": map[string]any{"type": "string"},
				"path":         map[string]any{"type": "string", "description": "Path relative to the directory root. Empty or '.' = root."},
			},
			"required": []string{"directory_id"},
		},
	},
	{
		"name":        "memory_write",
		"description": "Create or update a memory page. For git-backed directories the server commits and pushes.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"directory_id": map[string]any{"type": "string"},
				"path":         map[string]any{"type": "string"},
				"content":      map[string]any{"type": "string"},
				"message":      map[string]any{"type": "string", "description": "Optional commit message for git-backed directories."},
			},
			"required": []string{"directory_id", "path", "content"},
		},
	},
	{
		"name":        "memory_status",
		"description": "Report backend status for each visible directory (last sync, last error).",
		"inputSchema": map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	},
}

func (s *Server) handleToolsList(req *rpcReq) *rpcResp {
	return &rpcResp{
		Jsonrpc: "2.0",
		ID:      req.ID,
		Result:  map[string]any{"tools": toolsCatalog},
	}
}

// --- Tool execution ---

func (s *Server) handleToolsCall(conn *registry.Connector, req *rpcReq) *rpcResp {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &rpcResp{
			Jsonrpc: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32602, Message: "invalid params: " + err.Error()},
		}
	}

	logs.Info("MCP tools/call %s from %q", params.Name, conn.Name)
	var (
		text  string
		isErr bool
	)
	switch params.Name {
	case "memory_load":
		text = s.activeMemorySection(conn)
	case "memory_directories":
		text = s.toolDirectories(conn)
	case "memory_list":
		text, isErr = s.toolListPath(conn, params.Arguments)
	case "memory_search":
		text, isErr = s.toolSearch(conn, params.Arguments)
	case "memory_read":
		text, isErr = s.toolRead(conn, params.Arguments)
	case "memory_write":
		text, isErr = s.toolWrite(conn, params.Arguments)
	case "memory_status":
		text = s.toolStatus(conn)
	default:
		return &rpcResp{
			Jsonrpc: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32601, Message: "unknown tool: " + params.Name},
		}
	}

	return &rpcResp{
		Jsonrpc: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": text},
			},
			"isError": isErr,
		},
	}
}

func (s *Server) toolDirectories(conn *registry.Connector) string {
	dirs := s.reg.DirectoriesForConnector(conn)
	if len(dirs) == 0 {
		return "(no directories accessible)"
	}
	var sb strings.Builder
	for _, d := range dirs {
		fmt.Fprintf(&sb, "id=%s  name=%q  backend=%s\n  description: %s\n",
			d.Directory.ID, d.Directory.Name, d.Directory.Backend, d.Directory.Description)
	}
	return sb.String()
}

func (s *Server) toolSearch(conn *registry.Connector, args json.RawMessage) (string, bool) {
	var a struct {
		Query       string `json:"query"`
		DirectoryID string `json:"directory_id"`
		Limit       int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "invalid arguments: " + err.Error(), true
	}
	if a.Query == "" {
		return "query is required", true
	}
	dirs := s.reg.DirectoriesForConnector(conn)
	if a.DirectoryID != "" {
		dirs = filterDirsByID(dirs, a.DirectoryID)
		if len(dirs) == 0 {
			return "directory not accessible: " + a.DirectoryID, true
		}
	}
	limit := a.Limit
	if limit <= 0 {
		limit = 50
	}
	var sb strings.Builder
	total := 0
	for _, d := range dirs {
		hits, err := d.Backend.Search(a.Query, limit-total)
		if err != nil {
			fmt.Fprintf(&sb, "[%s] error: %v\n", d.Directory.Name, err)
			continue
		}
		for _, h := range hits {
			fmt.Fprintf(&sb, "[%s] %s:%d  %s\n", d.Directory.ID, h.Path, h.Line, h.Snippet)
			total++
			if total >= limit {
				break
			}
		}
		if total >= limit {
			break
		}
	}
	if total == 0 {
		return "(no matches)", false
	}
	return sb.String(), false
}

func (s *Server) toolRead(conn *registry.Connector, args json.RawMessage) (string, bool) {
	var a struct {
		DirectoryID string `json:"directory_id"`
		Path        string `json:"path"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "invalid arguments: " + err.Error(), true
	}
	d := s.reg.DirectoryForConnector(conn, a.DirectoryID)
	if d == nil {
		return "directory not accessible: " + a.DirectoryID, true
	}
	data, err := d.Backend.Read(a.Path)
	if err != nil {
		return err.Error(), true
	}
	return string(data), false
}

func (s *Server) toolWrite(conn *registry.Connector, args json.RawMessage) (string, bool) {
	if !conn.Write {
		return "connector is read-only", true
	}
	var a struct {
		DirectoryID string `json:"directory_id"`
		Path        string `json:"path"`
		Content     string `json:"content"`
		Message     string `json:"message"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "invalid arguments: " + err.Error(), true
	}
	d := s.reg.DirectoryForConnector(conn, a.DirectoryID)
	if d == nil {
		return "directory not accessible: " + a.DirectoryID, true
	}
	if err := d.Backend.Write(a.Path, []byte(a.Content), a.Message); err != nil {
		return err.Error(), true
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(a.Content), a.Path), false
}

func (s *Server) toolListPath(conn *registry.Connector, args json.RawMessage) (string, bool) {
	var a struct {
		DirectoryID string `json:"directory_id"`
		Path        string `json:"path"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "invalid arguments: " + err.Error(), true
	}
	d := s.reg.DirectoryForConnector(conn, a.DirectoryID)
	if d == nil {
		return "directory not accessible: " + a.DirectoryID, true
	}
	entries, err := d.Backend.ListPath(a.Path)
	if err != nil {
		return err.Error(), true
	}
	if len(entries) == 0 {
		return "(empty)", false
	}
	var sb strings.Builder
	for _, e := range entries {
		if e.IsDir {
			children, _ := d.Backend.ListPath(e.Path)
			fmt.Fprintf(&sb, "%s/  (%d items)\n", e.Name, len(children))
		} else {
			fmt.Fprintf(&sb, "%s\n", e.Name)
		}
	}
	return sb.String(), false
}

func (s *Server) toolStatus(conn *registry.Connector) string {
	dirs := s.reg.DirectoriesForConnector(conn)
	if len(dirs) == 0 {
		return "(no directories accessible)"
	}
	var sb strings.Builder
	for _, d := range dirs {
		st := d.Backend.Status()
		fmt.Fprintf(&sb, "%s: backend=%s path=%s last_sync=%s\n",
			d.Directory.Name, st.Backend, st.Path, st.LastSync.Format("2006-01-02 15:04:05"))
		if st.LastError != "" {
			fmt.Fprintf(&sb, "  last_error: %s\n", st.LastError)
		}
	}
	return sb.String()
}

func filterDirsByID(dirs []registry.DirectoryView, id string) []registry.DirectoryView {
	for _, d := range dirs {
		if d.Directory.ID == id {
			return []registry.DirectoryView{d}
		}
	}
	return nil
}
