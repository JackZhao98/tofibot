package app

import (
	"github.com/JackZhao98/tofibot/internal/computer"
	"net/http"
)

// Saving desired capacity is non-disruptive; applying requires a separate confirmation.
func (s *Server) routeComputerResources(w http.ResponseWriter, r *http.Request, path string) bool {
	if path != "computer/resources" && path != "computer/resources/apply" {
		return false
	}
	if s.microVM == nil {
		writeErr(w, 404, "not_configured", "computer VM is not configured")
		return true
	}
	if path == "computer/resources/apply" {
		if r.Method != http.MethodPost {
			writeErr(w, 405, "method_not_allowed", "use POST")
			return true
		}
		var value struct {
			Confirm bool `json:"confirm"`
		}
		if err := decodeStrict(r, 4096, &value); err != nil || !value.Confirm {
			writeErr(w, 400, "confirmation_required", "请确认重启共享电脑，所有桌面和终端将断开")
			return true
		}
		var active int
		if s.store == nil {
			writeErr(w, 503, "computer_unavailable", "无法确认任务状态")
			return true
		}
		if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE status IN ('queued','running')`).Scan(&active); err != nil {
			writeErr(w, 503, "computer_unavailable", "无法确认任务状态")
			return true
		}
		if active > 0 {
			writeErr(w, 409, "computer_busy", "Bot 仍有任务运行，请先停止任务后重试")
			return true
		}
		out, err := s.microVM.ApplyResources(r.Context())
		if err != nil {
			writeErr(w, 409, "restart_unavailable", "电脑暂时无法重启，请稍后重试："+err.Error())
		} else {
			writeJSON(w, 202, out)
		}
		return true
	}

	switch r.Method {
	case http.MethodGet:
		out, err := s.microVM.Resources(r.Context())
		if err != nil {
			writeErr(w, 503, "computer_unavailable", err.Error())
		} else {
			writeJSON(w, 200, out)
		}
	case http.MethodPut:
		var value computer.ResourceAllocation
		if err := decodeStrict(r, 4096, &value); err != nil {
			writeErr(w, 400, "invalid_request", "provide vcpus, memory_mib and disk_gib")
			return true
		}
		if value.VCPUs < 1 || value.VCPUs > 32 || value.MemoryMiB < 512 || value.MemoryMiB > 32768 || value.DiskGiB < 8 || value.DiskGiB > 1024 {
			writeErr(w, 400, "invalid_resources", "invalid computer resource allocation")
			return true
		}
		out, err := s.microVM.ConfigureResources(r.Context(), value)
		if err != nil {
			writeErr(w, 400, "resource_configuration_failed", err.Error())
		} else {
			writeJSON(w, 200, out)
		}
	default:
		writeErr(w, 405, "method_not_allowed", "use GET or PUT")
	}
	return true
}
