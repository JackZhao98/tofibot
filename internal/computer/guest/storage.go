package guest

import "net/http"

// StorageStats measures the mounted workspace filesystem, not a directory scan.
// Allocated image blocks and promised quota are separate host-side measurements.
type StorageStats struct {
	TotalBytes     uint64 `json:"total_bytes"`
	UsedBytes      uint64 `json:"used_bytes"`
	FreeBytes      uint64 `json:"free_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
}

func (s *Service) handleStorage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "storage path is fixed")
		return
	}
	stats, err := workspaceStorage(s.root)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "workspace storage metrics unavailable")
		return
	}
	writeJSON(w, http.StatusOK, stats)
}
