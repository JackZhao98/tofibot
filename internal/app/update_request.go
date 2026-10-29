package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Update from the web page (decision D69). The App only REQUESTS: it writes
// request.json into a directory shared with the host and reads status.json that
// the host (root, tofi-update-request.path) writes. The download, digest check,
// backup, switch and rollback stay in `tofi update`; nothing here is privileged.

const (
	autoUpdateChannelRC = "update_channel_rc" // stored like an automation setting, read from the root server in every mode
	updateRequestFile   = "request.json"
	updateStatusFile    = "status.json"
	updateHostFile      = "host.json"
	updateStatusLimit   = 16 << 10
	// An active status older than this is a host that died mid-update; it no longer blocks a new request.
	updateStatusStale = 6 * time.Hour
)

var (
	updateRequestMu sync.Mutex
	updateNonceRE   = regexp.MustCompile(`^[0-9a-f]{32}$`)
	updateStates    = map[string]bool{"queued": true, "waiting_for_runs": true, "downloading": true, "verifying": true, "switching": true, "done": true, "failed": true, "rolled_back": true}
	updateActive    = map[string]bool{"queued": true, "waiting_for_runs": true, "downloading": true, "verifying": true, "switching": true}
	updateCodeRE    = regexp.MustCompile(`^[a-z][a-z_]{0,48}$`)
)

type updateRequestInfo struct {
	Version     string `json:"version"`
	Channel     string `json:"channel"`
	RequestedAt string `json:"requested_at"`
}

type updateStatusInfo struct {
	State        string `json:"state"`
	Version      string `json:"version,omitempty"`
	From         string `json:"from,omitempty"`
	Channel      string `json:"channel,omitempty"`
	Progress     *int   `json:"progress"`
	Code         string `json:"code,omitempty"`
	RolledBackTo string `json:"rolled_back_to,omitempty"`
	RequestedAt  string `json:"requested_at,omitempty"`
	StartedAt    string `json:"started_at,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
	FinishedAt   string `json:"finished_at,omitempty"`
}

type updateResponse struct {
	updateNotice
	ChannelRCEnabled bool               `json:"channel_rc_enabled"`
	LatestRC         string             `json:"latest_rc"`
	LatestRCNotesURL string             `json:"latest_rc_notes_url"`
	HostUpdate       bool               `json:"host_update"`
	Request          *updateRequestInfo `json:"request"`
	Status           *updateStatusInfo  `json:"status"`
}

func updateDirs() (request, status string) {
	return os.Getenv("TOFI_UPDATE_REQUEST_DIR"), os.Getenv("TOFI_UPDATE_STATUS_DIR")
}

func (s *Server) updateChannelRC() bool {
	v, ok := s.autoStored(autoUpdateChannelRC)
	return ok && v == "true"
}

// hostUpdateAvailable is true when both directories are mounted and the host has
// announced (host.json) that its request unit is installed.
func hostUpdateAvailable() bool {
	req, status := updateDirs()
	if req == "" || status == "" {
		return false
	}
	if info, err := os.Stat(req); err != nil || !info.IsDir() {
		return false
	}
	raw, ok := readSmallFile(filepath.Join(status, updateHostFile), 4<<10)
	if !ok {
		return false
	}
	var marker struct {
		UpdateRequest int `json:"update_request"`
	}
	return json.Unmarshal(raw, &marker) == nil && marker.UpdateRequest == 1
}

func readSmallFile(path string, limit int64) ([]byte, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, false
	}
	return raw, true
}

// pendingUpdateRequest reads request.json (still waiting for the host).
func pendingUpdateRequest() *updateRequestInfo {
	req, _ := updateDirs()
	if req == "" {
		return nil
	}
	raw, ok := readSmallFile(filepath.Join(req, updateRequestFile), 4<<10)
	if !ok {
		return nil
	}
	var in updateRequestInfo
	if json.Unmarshal(raw, &in) != nil || !validUpdateRequest(in.Version, in.Channel) {
		return nil
	}
	return &in
}

func validUpdateRequest(version, channel string) bool {
	if _, ok := releaseVersionKey(version); !ok {
		return false
	}
	return (channel == "stable" && !strings.Contains(version, "-rc.")) || (channel == "rc" && strings.Contains(version, "-rc."))
}

// hostUpdateStatus reads the host's status.json and keeps only the fields and values the page may show.
func hostUpdateStatus() *updateStatusInfo {
	_, dir := updateDirs()
	if dir == "" {
		return nil
	}
	raw, ok := readSmallFile(filepath.Join(dir, updateStatusFile), updateStatusLimit)
	if !ok {
		return nil
	}
	var in updateStatusInfo
	if json.Unmarshal(raw, &in) != nil || !updateStates[in.State] {
		return nil
	}
	for _, v := range []*string{&in.Version, &in.From, &in.RolledBackTo} {
		if _, ok := releaseVersionKey(*v); !ok {
			*v = ""
		}
	}
	if in.Channel != "stable" && in.Channel != "rc" {
		in.Channel = ""
	}
	if !updateCodeRE.MatchString(in.Code) {
		in.Code = ""
	}
	if in.Progress != nil && (*in.Progress < 0 || *in.Progress > 100) {
		in.Progress = nil
	}
	for _, v := range []*string{&in.RequestedAt, &in.StartedAt, &in.UpdatedAt, &in.FinishedAt} {
		if _, err := time.Parse(time.RFC3339, *v); err != nil {
			*v = ""
		}
	}
	return &in
}

func updateStatusBlocks(st *updateStatusInfo, now time.Time) bool {
	if st == nil || !updateActive[st.State] {
		return false
	}
	at, err := time.Parse(time.RFC3339, st.UpdatedAt)
	return err != nil || now.Sub(at) < updateStatusStale
}

func (s *Server) updateSnapshot(c *updateChecker) updateResponse {
	rc := s.updateChannelRC()
	out := updateResponse{updateNotice: c.snapshot(), ChannelRCEnabled: rc, HostUpdate: hostUpdateAvailable()}
	out.LatestRC, out.LatestRCNotesURL = c.rcSnapshot(rc)
	if out.HostUpdate {
		out.Request = pendingUpdateRequest()
		out.Status = hostUpdateStatus()
	}
	return out
}

// serveSystemUpdate answers /api/system/update[/request|/settings]. The caller
// has enforced the signed-in owner (single mode) or an Admin account (accounts
// mode); `s` is the root server, which holds the update settings in both modes.
func (s *Server) serveSystemUpdate(w http.ResponseWriter, r *http.Request, rest string) {
	switch rest {
	case "":
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, s.updateSnapshot(sharedUpdates()))
	case "check":
		// "Check for updates": read the release manifest now instead of waiting for the cached read to age out.
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
			return
		}
		c := sharedUpdates()
		if err := c.checkNow(r.Context(), s.updateChannelRC()); err != nil {
			writeErr(w, http.StatusBadGateway, "update_check_failed", "the update server could not be reached")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, s.updateSnapshot(c))
	case "request":
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
			return
		}
		s.requestSystemUpdate(w, r, sharedUpdates())
	case "settings":
		if r.Method != http.MethodPut {
			w.Header().Set("Allow", http.MethodPut)
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use PUT")
			return
		}
		var in struct {
			ChannelRC *bool `json:"channel_rc"`
		}
		if decodeStrict(r, 1<<10, &in) != nil || in.ChannelRC == nil {
			writeErr(w, http.StatusBadRequest, "invalid_request", "send channel_rc as true or false")
			return
		}
		var value *string
		if *in.ChannelRC {
			on := "true"
			value = &on
		}
		if s.store == nil || s.saveAutomationValue(autoUpdateChannelRC, value) != nil {
			writeErr(w, http.StatusInternalServerError, "storage", "cannot save the setting")
			return
		}
		writeJSON(w, http.StatusOK, s.updateSnapshot(sharedUpdates()))
	default:
		writeErr(w, http.StatusNotFound, "not_found", "unknown update route")
	}
}

func (s *Server) requestSystemUpdate(w http.ResponseWriter, r *http.Request, c *updateChecker) {
	var in struct {
		Version string `json:"version"`
		Channel string `json:"channel"`
	}
	if decodeStrict(r, 1<<10, &in) != nil || !validUpdateRequest(in.Version, in.Channel) {
		writeErr(w, http.StatusBadRequest, "invalid_request", "send a release version and its channel")
		return
	}
	rcOn := s.updateChannelRC()
	if in.Channel == "rc" && !rcOn {
		writeErr(w, http.StatusConflict, "update_channel_disabled", "release candidates are turned off")
		return
	}
	// Only what this server itself offers: the cached latest stable, or the cached newest rc when the channel is on.
	offered := ""
	if in.Channel == "stable" {
		if n := c.snapshot(); n.UpdateAvailable {
			offered = n.Latest
		}
	} else {
		offered, _ = c.rcSnapshot(true)
	}
	if offered == "" || offered != in.Version {
		writeErr(w, http.StatusConflict, "update_not_available", "that version is not the one on offer")
		return
	}
	if !hostUpdateAvailable() {
		writeErr(w, http.StatusServiceUnavailable, "host_update_unavailable", "this server cannot update from the page; run sudo tofi update on the host")
		return
	}
	updateRequestMu.Lock()
	defer updateRequestMu.Unlock()
	if pendingUpdateRequest() != nil || updateStatusBlocks(hostUpdateStatus(), time.Now()) {
		writeErr(w, http.StatusConflict, "update_in_progress", "an update is already requested")
		return
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "cannot create the request")
		return
	}
	body, _ := json.Marshal(map[string]string{
		"version":      in.Version,
		"channel":      in.Channel,
		"requested_at": time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		"nonce":        hex.EncodeToString(nonce),
	})
	if err := writeUpdateRequest(body); err != nil {
		if os.IsExist(err) {
			writeErr(w, http.StatusConflict, "update_in_progress", "an update is already requested")
			return
		}
		writeErr(w, http.StatusServiceUnavailable, "host_update_unavailable", "cannot write the update request")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"request": updateRequestInfo{Version: in.Version, Channel: in.Channel, RequestedAt: time.Now().UTC().Format(time.RFC3339)}})
}

// writeUpdateRequest creates request.json atomically and only if it does not exist:
// the full file is written under a temporary name and then hard-linked into place,
// so the host never reads half a request and a second request cannot replace one.
func writeUpdateRequest(body []byte) error {
	dir, _ := updateDirs()
	tmp, err := os.CreateTemp(dir, ".request-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Link(name, filepath.Join(dir, updateRequestFile))
}
