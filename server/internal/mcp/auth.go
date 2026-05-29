package mcp

import (
	"net/http"
	"strings"

	"github.com/sudiptadeb/memd/server/internal/registry"
)

const bearerPrefix = "bearer "

func mountPrefix(prefix string) string {
	prefix = "/" + strings.Trim(prefix, "/")
	if prefix == "/" {
		return prefix
	}
	return prefix
}

func bearerToken(r *http.Request) string {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(auth) <= len(bearerPrefix) || !strings.EqualFold(auth[:len(bearerPrefix)], bearerPrefix) {
		return ""
	}
	return strings.TrimSpace(auth[len(bearerPrefix):])
}

func tokenLike(segment string) bool {
	if len(segment) != 32 {
		return false
	}
	for _, r := range segment {
		if (r < 'a' || r > 'z') && (r < '2' || r > '7') {
			return false
		}
	}
	return true
}

func pathTail(path, prefix string) (string, bool) {
	prefix = mountPrefix(prefix)
	if path == prefix {
		return "", true
	}
	withSlash := prefix + "/"
	if strings.HasPrefix(path, withSlash) {
		return strings.TrimPrefix(path, withSlash), true
	}
	return "", false
}

func (s *Server) connectorFromRequest(r *http.Request, prefix, kind string) (*registry.Connector, string, bool) {
	tail, ok := pathTail(r.URL.Path, prefix)
	if !ok {
		return nil, "", false
	}
	tail = strings.Trim(tail, "/")
	authToken := bearerToken(r)
	if tail == "" {
		conn := s.connectorByToken(authToken, kind)
		return conn, "", conn != nil
	}

	first, rest, hasRest := strings.Cut(tail, "/")
	if urlConn := s.reg.ConnectorByToken(first); urlConn != nil {
		if urlConn.EffectiveKind() != kind {
			return nil, "", false
		}
		if authToken != "" && authToken != first {
			return nil, "", false
		}
		if !hasRest {
			rest = ""
		}
		return urlConn, rest, true
	}
	if tokenLike(first) {
		return nil, "", false
	}

	conn := s.connectorByToken(authToken, kind)
	if conn == nil {
		return nil, "", false
	}
	return conn, tail, true
}

func (s *Server) connectorByToken(tok, kind string) *registry.Connector {
	if tok == "" {
		return nil
	}
	conn := s.reg.ConnectorByToken(tok)
	if conn == nil || conn.EffectiveKind() != kind {
		return nil
	}
	return conn
}
