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

	"github.com/sudiptadeb/memd/server/internal/registry"
)

const protocolVersion = "2024-11-05"

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
		// notification — no response body
		w.WriteHeader(http.StatusOK)
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
		return s.handleInitialize(req)
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

func (s *Server) handleInitialize(req *rpcReq) *rpcResp {
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

// --- Tool catalog ---

var toolsCatalog = []map[string]any{
	{
		"name":        "memory_directories",
		"description": "List the memory directories this connector can access.",
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
				"path":         map[string]any{"type": "string", "description": "Page path relative to the directory root (e.g. 'index.md' or 'subfolder/page.md')."},
			},
			"required": []string{"directory_id", "path"},
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

	var (
		text  string
		isErr bool
	)
	switch params.Name {
	case "memory_directories":
		text = s.toolDirectories(conn)
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
