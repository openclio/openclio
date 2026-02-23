package gateway

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/openclio/openclio/internal/storage"
)

// MemoryNodes handles GET /api/v1/memory/nodes.
func (h *Handlers) MemoryNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	if h.knowledgeGraph == nil {
		writeError(w, http.StatusServiceUnavailable, "knowledge graph is unavailable")
		return
	}

	limit := parseLimitParam(r, 100, 500)
	nodes, err := h.knowledgeGraph.ListNodes(limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list memory nodes: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"nodes": nodes,
		"count": len(nodes),
	})
}

// MemoryEdges handles GET /api/v1/memory/edges.
func (h *Handlers) MemoryEdges(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	if h.knowledgeGraph == nil {
		writeError(w, http.StatusServiceUnavailable, "knowledge graph is unavailable")
		return
	}

	limit := parseLimitParam(r, 100, 500)
	edges, err := h.knowledgeGraph.ListEdges(limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list memory edges: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"edges": edges,
		"count": len(edges),
	})
}

// MemorySearch handles GET /api/v1/memory/search?q=...&type=...
func (h *Handlers) MemorySearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "use GET")
		return
	}
	if h.knowledgeGraph == nil {
		writeError(w, http.StatusServiceUnavailable, "knowledge graph is unavailable")
		return
	}

	q := strings.TrimSpace(r.URL.Query().Get("q"))
	nodeType := strings.TrimSpace(r.URL.Query().Get("type"))
	limit := parseLimitParam(r, 100, 500)
	nodes, err := h.knowledgeGraph.SearchNodes(q, nodeType, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to search memory nodes: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"nodes": nodes,
		"count": len(nodes),
		"query": q,
		"type":  nodeType,
	})
}

// MemoryNodeDelete handles DELETE /api/v1/memory/nodes/{id}.
func (h *Handlers) MemoryNodeDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "use DELETE")
		return
	}
	if h.knowledgeGraph == nil {
		writeError(w, http.StatusServiceUnavailable, "knowledge graph is unavailable")
		return
	}

	idRaw := strings.TrimSpace(extractPathParam(r.URL.Path, "/api/v1/memory/nodes/"))
	if idRaw == "" || strings.Contains(idRaw, "/") {
		writeError(w, http.StatusBadRequest, "missing node id")
		return
	}
	id, err := strconv.ParseInt(idRaw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid node id")
		return
	}

	if err := h.knowledgeGraph.DeleteNode(id); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "memory node not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete memory node: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"deleted": id,
	})
}

func parseLimitParam(r *http.Request, defaultLimit, maxLimit int) int {
	if r == nil {
		return defaultLimit
	}
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return defaultLimit
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return defaultLimit
	}
	if maxLimit > 0 && n > maxLimit {
		return maxLimit
	}
	return n
}
