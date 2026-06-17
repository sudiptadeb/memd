// Package graph builds the link graph of a memory directory: nodes are memory
// files, edges are the markdown links between them. It is the shared
// foundation for the agent-facing memory_graph tool and the human-facing
// visual navigator in the web dashboard. The generated index.md projection is
// a one-hop view of this same graph.
package graph

import (
	"path"
	"sort"
	"strings"

	"github.com/sudiptadeb/memd/server/internal/storage"
)

// Node is one memory file in the graph.
type Node struct {
	Path        string `json:"path"`
	Type        string `json:"type,omitempty"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Inbound     int    `json:"inbound"`
	Outbound    int    `json:"outbound"`
}

// Edge is a directed markdown link from one file to another. Broken is true
// when the target does not resolve to a file that exists in the directory.
type Edge struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Broken bool   `json:"broken"`
}

// Graph is the full link graph plus a few precomputed summaries.
type Graph struct {
	Nodes   []Node `json:"nodes"`
	Edges   []Edge `json:"edges"`
	Orphans []string `json:"orphans"` // concept files with no links in or out
	Broken  []Edge   `json:"broken"`  // edges whose target is missing
}

// Build walks every file in the backend and assembles the link graph. Files
// named index.md are skipped: they are generated scaffolding, not concepts,
// and linking through them would drown the real relationships in noise.
func Build(b storage.Backend) (*Graph, error) {
	paths, err := b.List()
	if err != nil {
		return nil, err
	}

	nodeIdx := map[string]*Node{}
	var order []string
	exists := map[string]bool{} // every file, so edges to non-concept files (csv, etc.) resolve
	for _, p := range paths {
		exists[p] = true
	}
	for _, p := range paths {
		if path.Base(p) == storage.GeneratedIndexName {
			continue
		}
		n := &Node{Path: p, Title: path.Base(p)}
		if isManaged(p) {
			if raw, err := b.ReadRaw(p); err == nil {
				pg := storage.ParsePage(raw)
				fields := storage.FieldsFromAgentFM(pg.AgentFM)
				n.Type = fields["type"]
				n.Description = fields["description"]
				if t := fields["title"]; t != "" {
					n.Title = t
				} else if h1 := firstH1(pg.Body); h1 != "" {
					n.Title = h1
				}
			}
		}
		nodeIdx[p] = n
		order = append(order, p)
	}

	var edges []Edge
	for _, p := range order {
		if !isManaged(p) {
			continue
		}
		raw, err := b.ReadRaw(p)
		if err != nil {
			continue
		}
		pg := storage.ParsePage(raw)
		for _, target := range storage.ExtractMarkdownLinks(pg.Body) {
			to := resolveTarget(p, target)
			broken := !exists[to]
			edges = append(edges, Edge{From: p, To: to, Broken: broken})
			nodeIdx[p].Outbound++
			if n := nodeIdx[to]; n != nil {
				n.Inbound++
			}
		}
	}

	g := &Graph{}
	for _, p := range order {
		g.Nodes = append(g.Nodes, *nodeIdx[p])
	}
	g.Edges = edges
	for _, e := range edges {
		if e.Broken {
			g.Broken = append(g.Broken, e)
		}
	}
	for _, n := range g.Nodes {
		if n.Inbound == 0 && n.Outbound == 0 && path.Base(n.Path) != "MEMORY.md" {
			g.Orphans = append(g.Orphans, n.Path)
		}
	}
	sort.Strings(g.Orphans)
	return g, nil
}

// Neighbors returns the outbound and inbound links of one node.
func (g *Graph) Neighbors(p string) (out, in []string) {
	for _, e := range g.Edges {
		if e.From == p {
			out = append(out, e.To)
		}
		if e.To == p {
			in = append(in, e.From)
		}
	}
	sort.Strings(out)
	sort.Strings(in)
	return out, in
}

func isManaged(p string) bool {
	l := strings.ToLower(p)
	return strings.HasSuffix(l, ".md") || strings.HasSuffix(l, ".html") || strings.HasSuffix(l, ".htm")
}

// resolveTarget resolves a markdown link target relative to the linking file's
// directory and returns a clean, directory-relative path.
func resolveTarget(fromFile, target string) string {
	base := path.Dir(fromFile)
	if base == "." {
		base = ""
	}
	joined := path.Clean(path.Join(base, target))
	return strings.TrimPrefix(joined, "./")
}

func firstH1(body []byte) string {
	for _, line := range strings.Split(string(body), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "# ") {
			return strings.TrimSpace(t[2:])
		}
	}
	return ""
}
