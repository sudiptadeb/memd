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
	case "prompts/list":
		return s.handlePromptsList(req)
	case "prompts/get":
		logs.Info("MCP prompts/get from connector %q", conn.Name)
		return s.handlePromptsGet(conn, req)
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
				"tools":   map[string]any{},
				"prompts": map[string]any{},
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
	// Storage tools — agent-internal primitives. The agent calls these to
	// read and write memory while executing user requests or workflows.
	// Users should invoke the memd_* workflow tools (or the equivalent
	// slash-command prompts), not these.
	{
		"name":        "memory_load",
		"description": "[Agent-internal storage primitive.] MUST be called once at the start of every conversation, before responding to anything else. Returns your active memory — every accessible directory's description, page listing, and the full contents of its top-level MEMORY.md. Treat its result as memory you already know.",
		"inputSchema": map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	},
	{
		"name":        "memory_directories",
		"description": "[Agent-internal storage primitive.] List the memory directories this connector can access (no content). Rarely needed — memory_load returns more.",
		"inputSchema": map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	},
	{
		"name":        "memory_search",
		"description": "[Agent-internal storage primitive.] Search memory pages for a query. Returns matching lines with file paths.",
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
		"description": "[Agent-internal storage primitive.] Read one memory page in full. Bumps the page's last_read_at and access_count in its memd: front matter.",
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
		"description": "[Agent-internal storage primitive.] List the direct children of a path inside a memory directory. Use to dive into a folder the Active Memory topology shows by name. Pass an empty path (or omit it) to list the directory root.",
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
		"description": "[Agent-internal storage primitive.] Create or update a memory page. For git-backed directories the server debounces commit + push. Any memd: front-matter block in the content is discarded; the server owns that subtree.",
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
		"description": "[Agent-internal storage primitive.] Report backend status for each visible directory (last sync, last error).",
		"inputSchema": map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	},
	// Workflow tools — equivalent to the MCP prompts of the same root name.
	// MCP prompts only surface as slash commands in some clients (Claude
	// Code yes; Codex CLI no). Exposing the same workflows as tools means
	// every client can invoke them. Distinct namespace (`memd_`) keeps
	// them visually separate from the storage tools (`memory_`).
	{
		"name":        "memd_reorganise",
		"description": "Workflow: rearrange the shelves — restructure existing memory, group root pages into folders, rewrite MEMORY.md as a curated sectioned index. Returns the workflow body; follow its steps. Same as the /<connector>:reorganise prompt.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"directory_id": map[string]any{"type": "string", "description": "Optional: the directory to reorganise."},
			},
		},
	},
	{
		"name":        "memd_harvest",
		"description": "Workflow: bring in the crop — gather knowledge from sources OUTSIDE memd (Claude auto-memory, Cursor rules, raw notes, another memd directory) and integrate via ADD/UPDATE/DELETE/NONE. Dispatches to background agent when available. Returns the workflow body; follow its steps. Same as the /<connector>:harvest prompt.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"directory_id": map[string]any{"type": "string", "description": "Optional: the directory to harvest into."},
			},
		},
	},
	{
		"name":        "memd_dream",
		"description": "Workflow: sleep consolidation — forget unused / contradicted pages, cement what was referenced this session. Uses per-page memd: stats. Dispatches to background agent when available. Returns the workflow body; follow its steps. Same as the /<connector>:dream prompt.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"directory_id": map[string]any{"type": "string", "description": "Optional: the directory to dream over."},
			},
		},
	},
	{
		"name":        "memd_recall",
		"description": "Workflow: reminisce on a topic — search, walk linked pages, synthesise an answer. Returns the workflow body; follow its steps. Same as the /<connector>:recall prompt.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"topic":        map[string]any{"type": "string", "description": "What to recall (free text)."},
				"directory_id": map[string]any{"type": "string", "description": "Optional: restrict to one directory."},
			},
			"required": []string{"topic"},
		},
	},
	{
		"name":        "memd_housekeep",
		"description": "Workflow: daily tidying — fix dangling links, orphan pages, missing front matter, stale last_reorganised. Dispatches to background agent when available. Returns the workflow body; follow its steps. Same as the /<connector>:housekeep prompt.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"directory_id": map[string]any{"type": "string", "description": "Optional: the directory to housekeep."},
			},
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
	case "memd_reorganise", "memd_harvest", "memd_dream", "memd_recall", "memd_housekeep":
		text, isErr = s.toolWorkflow(params.Name, params.Arguments)
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

// --- Prompts ---

// promptsCatalog lists workflows as MCP prompts (slash commands) with NO
// arguments declared. Some clients (Claude Code) treat any declared
// argument as a UI gate, blocking the slash command on user input even
// when the argument is marked optional. Skipping the declaration fires
// the prompt immediately; the body itself asks for whatever it needs.
// The memd_* tool catalog still accepts JSON args.
var promptsCatalog = []map[string]any{
	{
		"name":        "reorganise",
		"description": "Rearrange the shelves: restructure existing memory, group root pages into folders, rewrite MEMORY.md as a clean curated index, bump last_reorganised.",
	},
	{
		"name":        "harvest",
		"description": "Bring in the crop: gather knowledge from external sources (Claude auto-memory, Cursor rules, raw notes, another memd directory) and integrate via ADD/UPDATE/DELETE/NONE.",
	},
	{
		"name":        "dream",
		"description": "Sleep consolidation: forget unused / contradicted pages, cement what was referenced this session. Uses the per-page memd: stats (last_read_at, access_count) to decide.",
	},
	{
		"name":        "recall",
		"description": "Reminisce on a topic: search memory, walk linked pages, and synthesise an answer rather than dumping raw search hits.",
	},
	{
		"name":        "housekeep",
		"description": "Daily tidying: find structural drift — dangling links, orphan pages, missing front matter, stale last_reorganised. Fix in place autonomously.",
	},
}

func (s *Server) handlePromptsList(req *rpcReq) *rpcResp {
	return &rpcResp{
		Jsonrpc: "2.0",
		ID:      req.ID,
		Result:  map[string]any{"prompts": promptsCatalog},
	}
}

func (s *Server) handlePromptsGet(_ *registry.Connector, req *rpcReq) *rpcResp {
	var params struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &rpcResp{
			Jsonrpc: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32602, Message: "invalid params: " + err.Error()},
		}
	}
	body, desc, ok := workflowBody(params.Name, params.Arguments)
	if !ok {
		return &rpcResp{
			Jsonrpc: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32602, Message: "unknown prompt: " + params.Name},
		}
	}
	return &rpcResp{
		Jsonrpc: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"description": desc,
			"messages":    promptMessage(body),
		},
	}
}

// workflowBody returns the prompt text and short description for a named
// workflow. Used both by prompts/get and by the equivalent memory_*
// workflow tools (so clients that don't surface MCP prompts as slash
// commands — e.g. Codex CLI — can still trigger the same workflow via
// tools/call).
func workflowBody(name string, args map[string]string) (text, description string, ok bool) {
	switch name {
	case "reorganise":
		return reorganiseText(args), "Rearrange the shelves.", true
	case "harvest":
		return harvestText(args), "Bring in the crop.", true
	case "dream":
		return dreamText(args), "Sleep consolidation.", true
	case "recall":
		return recallText(args), "Reminisce on a topic.", true
	case "housekeep":
		return housekeepText(args), "Daily tidying.", true
	}
	return "", "", false
}

func reorganiseText(args map[string]string) string {
	return fmt.Sprintf(backgroundPreamble+`Run a focused reorganisation pass on memd memory.

1. If you have not already in this session, call %smemory_load()%s.
2. %s
3. Walk every page with %smemory_list%s and %smemory_read%s. For each, decide:
   - duplicated or redundant → merge into the better page.
   - stale or superseded → archive under %smemory/_archive/%s with a one-line historical note in MEMORY.md if it still matters.
   - related to others → group under a descriptive multi-word folder.
4. Perform the moves and rewrites with %smemory_write%s. (memd has no rename — write the new path; if the old path is no longer needed, write an empty body... actually leave the old in place and archive it instead.)
5. Rewrite MEMORY.md as a curated sectioned index per the doctrine's "Curate, don't enumerate" rule.
6. Update MEMORY.md's agent front matter: %slast_reorganised%s = today, %sentries%s = the final one-liner count.
7. Report the diff: counts of pages added / moved / archived / merged; the new MEMORY.md section headers; anything skipped.

Only stop for user input if the *Run in background* preamble's drastic-action triggers apply (e.g. you're about to delete prose the user wrote, or restructure more than a third of the directory in one go).`,
		"`", "`",
		dirHint(args),
		"`", "`",
		"`", "`",
		"`", "`",
		"`", "`",
		"`", "`",
		"`", "`",
	)
}

func dirHint(args map[string]string) string {
	if id := strings.TrimSpace(args["directory_id"]); id != "" {
		return fmt.Sprintf("Target directory id: `%s`.", id)
	}
	return "If multiple directories are accessible, ask the user which one to operate on."
}

func promptMessage(text string) []map[string]any {
	return []map[string]any{
		{
			"role": "user",
			"content": map[string]any{
				"type": "text",
				"text": text,
			},
		},
	}
}

// backgroundPreamble is prepended to long-running passes (harvest, dream,
// housekeep) so the agent dispatches them to a background worker when one
// is available — keeping the main conversation responsive — and so the
// agent acts autonomously instead of stopping for approval ceremonies.
const backgroundPreamble = `**Run in background if you can. Act autonomously; report when done.**

If your environment has a Task / subagent / background-agent capability (Claude Code's Task tool, Codex's worker, Cursor's background agent), dispatch this entire pass to one and immediately return control to the user.

**Default to acting.** Do the work, then report what was done. Don't stop to ask permission for ordinary changes — every change is tracked in git and the user can review or revert.

**Only ask the user BEFORE writing when the change is *drastic*:**

- **Deleting a page** (prefer archiving — move to ` + "`memory/_archive/`" + ` — over deleting, since archives preserve content).
- **Removing prose the user themselves wrote**, or large prose blocks (more than a paragraph) from any page.
- **Overwriting a page tagged ` + "`priority: load-bearing`" + `** when your new content contradicts it.
- **Restructuring more than a third of the directory in one pass** (mass moves and large folder reshuffles deserve a heads-up).

For everything else — adding new pages, archiving stale ones, fixing links, rewriting MEMORY.md, promoting/demoting between sections, adding cross-links — proceed without asking.

When you finish, report:
- What you did (counts plus a one-line summary of each significant change).
- What you skipped and why.
- Anything you flagged for user attention.

If no background-agent capability is available, run inline using the same autonomous policy.

---

`

func harvestText(args map[string]string) string {
	return fmt.Sprintf(backgroundPreamble+"Run a `harvest` pass on memd memory — bring in the crop.\n\n"+
		"Goal: gather durable knowledge from sources OUTSIDE memd (your other memory systems — Claude's auto-memory, Cursor rules, paste-in notes, another memd directory, prior session context) and integrate it INTO memd.\n\n"+
		"1. Call `memory_load()` so you see the current state of memd memory.\n"+
		"2. %s\n"+
		"3. List the external sources you can see right now. Examples:\n"+
		"   - Claude Code's `CLAUDE.md` / `AGENTS.md` auto-memory.\n"+
		"   - Cursor's `.cursorrules` or rules pages.\n"+
		"   - Notes the user has pasted into this conversation.\n"+
		"   - Facts inferred from prior session context.\n"+
		"4. For each candidate fact:\n"+
		"   - `memory_search` for related existing pages.\n"+
		"   - Decide **ADD / UPDATE / DELETE / NONE**.\n"+
		"   - ADD → write a new page under `memory/` and add a one-line MEMORY.md entry.\n"+
		"   - UPDATE → edit the existing page in place.\n"+
		"   - DELETE → only when the new info clearly invalidates the old. Archive (move to `memory/_archive/`) instead of deleting wherever possible; add a one-line supersession note.\n"+
		"   - NONE → skip.\n"+
		"5. Do the writes as you go — don't stop for approval ceremonies. Drastic actions (per the preamble) still wait.\n"+
		"6. Report a summary at the end: ADD count, UPDATE count, ARCHIVE/DELETE count, with one-line descriptions of each significant change. List anything you skipped or flagged.", dirHint(args))
}

func dreamText(args map[string]string) string {
	return fmt.Sprintf(backgroundPreamble+"Run a `dream` pass on memd memory — sleep consolidation.\n\n"+
		"Goal: for this session, **cement** (load-bearing, recently-used) and **fade** (unused, contradicted, superseded). Use each page's `memd:` front matter as signal.\n\n"+
		"1. Call `memory_load()` to see the current state.\n"+
		"2. %s\n"+
		"3. For every page, `memory_read` it and inspect the `memd:` block:\n"+
		"   - `last_read_at` — when was this last accessed?\n"+
		"   - `access_count` — how often is it used?\n"+
		"   - `updated_at` — when did its body last change?\n"+
		"4. Act:\n"+
		"   - **Cement** — high `access_count`, recent `last_read_at`, or referenced this session. Pull into MEMORY.md's top sections; add cross-links from related pages. Do it.\n"+
		"   - **Fade** — `last_read_at` > 90 days, `access_count` 0–1, not linked from MEMORY.md. Archive (move under `memory/_archive/`) with a one-line supersession note in MEMORY.md if it still matters historically. Do it.\n"+
		"   - **Resolve contradictions** — if two pages disagree and the recent session confirmed one, supersede the other in place.\n"+
		"   - Drastic actions (deleting prose the user wrote, removing >1 paragraph, overwriting a `priority: load-bearing` page) — ask first per the preamble.\n"+
		"5. Report the diff: counts of pages cemented / faded / merged; pages skipped because the signal was ambiguous.\n\n"+
		"Stats are signal, not gospel. A rarely-read page can still be load-bearing (e.g. a once-a-year procedure). Use judgement; if a page's `priority` field says `load-bearing` or `reference`, treat low access_count as expected and leave it alone.", dirHint(args))
}

func recallText(args map[string]string) string {
	topic := strings.TrimSpace(args["topic"])
	if topic == "" {
		topic = "(no topic supplied — ask the user what they want to recall)"
	}
	return fmt.Sprintf("Run a `recall` pass on memd memory — reminisce on a topic.\n\n"+
		"Topic: **%s**\n\n"+
		"1. Call `memory_load()` if you haven't this session.\n"+
		"2. %s\n"+
		"3. Run `memory_search` for the topic and adjacent terms.\n"+
		"4. `memory_read` each promising hit.\n"+
		"5. Walk the in-page links — read related pages too.\n"+
		"6. Synthesise an answer for the user: what memd actually says about the topic, with page links. Cite the pages you used.\n\n"+
		"Don't dump raw search hits. Walk the wiki and present what you found.", topic, dirHint(args))
}

func housekeepText(args map[string]string) string {
	return fmt.Sprintf(backgroundPreamble+"Run a `housekeep` pass on memd memory — daily tidying.\n\n"+
		"Goal: find and fix **structural drift** without restructuring content. Housekeep is the most autonomous of the workflows — almost everything it does is reversible.\n\n"+
		"1. Call `memory_load()` to see the current state.\n"+
		"2. %s\n"+
		"3. Walk every page with `memory_read`. For each issue you find, fix it directly:\n"+
		"   - **Dangling links** — `MEMORY.md` references a missing page. Remove the entry (or add a redirect note if you can guess the new path).\n"+
		"   - **Orphan pages** — pages under `memory/` not linked from `MEMORY.md`. Add a one-line entry in the right section.\n"+
		"   - **Missing agent front matter** — add `topic` / `tags` / `related` where the page's subject makes them obvious.\n"+
		"   - **Stale `last_reorganised`** — flag in the report. Don't bump it yourself; that's `reorganise`'s job.\n"+
		"   - **Empty template sections** — delete the empty heading.\n"+
		"4. Report what you fixed and what you flagged (with reasoning).\n\n"+
		"Housekeep tidies; it doesn't restructure. If the directory needs structural change, recommend `reorganise` at the end of the report.", dirHint(args))
}

// --- Tool implementations ---

// toolWorkflow returns the body of a workflow (reorganise / harvest /
// dream / recall / housekeep) as a tool result. Equivalent to invoking
// the MCP prompt of the matching name. Tool name is "memd_<workflow>".
func (s *Server) toolWorkflow(toolName string, rawArgs json.RawMessage) (string, bool) {
	var a map[string]string
	if len(rawArgs) > 0 {
		_ = json.Unmarshal(rawArgs, &a)
	}
	if a == nil {
		a = map[string]string{}
	}
	workflow := strings.TrimPrefix(toolName, "memd_")
	body, _, ok := workflowBody(workflow, a)
	if !ok {
		return "unknown workflow: " + workflow, true
	}
	return body, false
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
