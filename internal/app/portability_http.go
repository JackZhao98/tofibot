package app

import (
	"context"
	"io"
	"net/http"
	"strings"
)

func (s *Server) routePortability(w http.ResponseWriter, r *http.Request, p string) bool {
	if !strings.HasPrefix(p, "portability/") {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	if p == "portability/capabilities" && r.Method == http.MethodGet {
		writeJSON(w, 200, map[string]any{"format": "tofi.bundle", "version": 3, "default_categories": portableDefaultCategories, "vault_environment_max_count": portableMaxEnvironmentRecords, "vault_environment_max_bytes": portableMaxEnvironmentBytes, "categories": portableCategories, "excluded": portableExcluded, "max_bytes": portableMaxBytes, "archives_supported": false, "attachment_max_bytes": portableMaxAttachmentBytes, "attachment_total_bytes": portableMaxAttachmentTotal, "attachment_max_count": portableMaxAttachments, "attachment_storage_ready": s.store.portableBlobBackend() != nil})
		return true
	}
	if p == "portability/environment" && r.Method == http.MethodGet {
		if !s.portableSensitiveRequest(w, r) {
			return true
		}
		items, e := s.store.portableEnvironmentInventory(r.Context())
		if e != nil {
			writeErr(w, 503, "recovery_unavailable", errPortableSecret.Error())
		} else {
			writeJSON(w, 200, map[string]any{"records": items})
		}
		return true
	}
	if strings.HasPrefix(p, "portability/recovery/") && r.Method == http.MethodDelete {
		if !s.portableSensitiveRequest(w, r) {
			return true
		}
		id := strings.TrimPrefix(p, "portability/recovery/")
		if !portableID(id) {
			writeErr(w, 400, "invalid_recovery", "Invalid recovery ID")
			return true
		}
		result, e := s.store.db.ExecContext(r.Context(), `DELETE FROM portability_secret_recovery WHERE id=?`, id)
		if e != nil {
			writeErr(w, 409, "recovery_unavailable", errPortableSecret.Error())
			return true
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			writeErr(w, 404, "not_found", "Recovery item not found")
		} else {
			writeJSON(w, 200, map[string]any{"ok": true, "status": "inactive_recovery_deleted"})
		}
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
		if containsPortable(in.Selection.Categories, portableEnvironmentCategory) || len(in.Selection.VaultEnvironmentIDs)+len(in.Selection.RecoveredEnvironmentIDs) > 0 {
			if !s.portableSensitiveRequest(w, r) {
				return true
			}
		}
		b, e := s.exportPortable(r.Context(), s.instance.ID, in.Selection, in.Kind)
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
	if err == nil && (len(b.VaultEnvironment) > 0 || containsPortable(in.Selection.Categories, portableEnvironmentCategory)) {
		if !s.portableSensitiveRequest(w, r) {
			return true
		}
	}
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
			message := e.Error()
			if len(b.VaultEnvironment) > 0 {
				message = errPortableSecret.Error()
			}
			writeErr(w, 409, "preview_unavailable", message)
			return true
		}
		writeJSON(w, 200, preview)
		return true
	}
	result, err := s.applyPortableDefaults(r.Context(), b, in.PreviewID)
	if err != nil {
		writeErr(w, 409, "import_not_applied", "Import was not applied. Preview again or check available storage.")
		return true
	}
	writeJSON(w, 200, result)
	return true
}

// Keep commit/cache publication ordered with ordinary settings writes. The
// helper returns with the mutex released before the caller writes any response.
func (s *Server) applyPortableDefaults(ctx context.Context, b portableBundle, previewID string) (portableResult, error) {
	if b.Settings == nil {
		return s.store.applyPortable(ctx, b, previewID)
	}
	result, _, err := s.store.applyPortableWithStateGuard(ctx, b, previewID, func() func(portableResult, bool, error) {
		s.mu.Lock()
		return func(result portableResult, replayed bool, err error) {
			if err == nil && !replayed {
				s.defaultModel, s.defaultReasoning = b.Settings.Model, b.Settings.ReasoningEffort
			}
			s.mu.Unlock()
		}
	})
	return result, err
}
