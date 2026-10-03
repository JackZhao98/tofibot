package app

import (
	"io"
	"net/http"
	"strings"
)

func (s *Server) routePortability(w http.ResponseWriter, r *http.Request, p string) bool {
	if !strings.HasPrefix(p, "portability/") {
		return false
	}
	if p == "portability/capabilities" && r.Method == http.MethodGet {
		writeJSON(w, 200, map[string]any{"format": "tofi.bundle", "version": 1, "categories": portableCategories, "excluded": portableExcluded, "max_bytes": portableMaxBytes, "archives_supported": false})
		return true
	}
	if r.Method != http.MethodPost {
		writeErr(w, 405, "method_not_allowed", "use POST")
		return true
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, portableMaxBytes+1))
	if err != nil || len(data) > portableMaxBytes {
		writeErr(w, 413, "bundle_too_large", "request exceeds the 16 MiB limit")
		return true
	}
	if p == "portability/export" {
		var in struct {
			Selection portableSelection `json:"selection"`
			Kind      string            `json:"kind"`
		}
		if err = portableJSON(data, &in); err != nil {
			writeErr(w, 400, "invalid_bundle", err.Error())
			return true
		}
		b, e := s.store.exportPortable(r.Context(), s.instance.ID, in.Selection, in.Kind)
		if e != nil {
			writeErr(w, 400, "export_unavailable", e.Error())
			return true
		}
		writeJSON(w, 200, b)
		return true
	}
	if p != "portability/preview" && p != "portability/apply" {
		writeErr(w, 404, "not_found", "not found")
		return true
	}
	var in portableImportRequest
	if err = portableJSON(data, &in); err != nil {
		writeErr(w, 400, "invalid_bundle", err.Error())
		return true
	}
	b, err := parsePortableBundle(in.Bundle)
	if err == nil {
		b, err = selectPortable(b, in.Selection)
	}
	if err != nil {
		writeErr(w, 400, "invalid_bundle", err.Error())
		return true
	}
	if p == "portability/preview" {
		preview, e := s.store.previewPortable(r.Context(), b)
		if e != nil {
			writeErr(w, 409, "preview_unavailable", e.Error())
			return true
		}
		writeJSON(w, 200, preview)
		return true
	}
	// Serialize settings commits and cache publication with ordinary settings writes.
	if b.Settings != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
	}
	result, replayed, err := s.store.applyPortableWithState(r.Context(), b, in.PreviewID)
	if err != nil {
		writeErr(w, 409, "import_not_applied", "Import was not applied. Preview again or check available storage.")
		return true
	}
	if b.Settings != nil && !replayed {
		s.defaultModel, s.defaultReasoning = b.Settings.Model, b.Settings.ReasoningEffort
	}
	writeJSON(w, 200, result)
	return true
}
