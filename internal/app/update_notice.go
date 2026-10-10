package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Version is the release tag baked in at build time:
//
//	go build -ldflags "-X github.com/JackZhao98/tofibot/internal/app.Version=v0.1.0"
//
// Source builds report "dev", which never counts as outdated.
var Version = "dev"

const (
	defaultUpdateManifestURL = "https://github.com/JackZhao98/tofibot/releases/latest/download/manifest.json"
	updateRefreshAfter       = 6 * time.Hour
	updateRetryAfter         = 10 * time.Minute
	updateFetchTimeout       = 15 * time.Second
	updateManifestLimit      = 1 << 20
)

var releaseVersionPattern = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)(?:-rc\.(\d+))?$`)

// releaseVersionKey mirrors version_key in deploy/self-host/tofi_host.py:
// vMAJOR.MINOR.PATCH[-rc.N], where a final release sorts after its rcs.
func releaseVersionKey(version string) ([5]int, bool) {
	m := releaseVersionPattern.FindStringSubmatch(version)
	if m == nil {
		return [5]int{}, false
	}
	var key [5]int
	for i := 0; i < 3; i++ {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return [5]int{}, false
		}
		key[i] = n
	}
	if m[4] == "" {
		key[3] = 1
	} else {
		n, err := strconv.Atoi(m[4])
		if err != nil {
			return [5]int{}, false
		}
		key[4] = n
	}
	return key, true
}

func releaseIsNewer(candidate, installed string) bool {
	n, ok1 := releaseVersionKey(candidate)
	o, ok2 := releaseVersionKey(installed)
	if !ok1 || !ok2 {
		return false
	}
	for i := range n {
		if n[i] != o[i] {
			return n[i] > o[i]
		}
	}
	return false
}

type updateNotice struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"update_available"`
	NotesURL        string `json:"notes_url"`
	CheckedAt       string `json:"checked_at"`
	AutoUpdate      string `json:"auto_update"`
}

// updateChecker holds the last good manifest result. Requests only read the
// cache; the network is touched by a single background refresh at a time.
type updateChecker struct {
	url     string
	current string
	client  *http.Client
	now     func() time.Time

	mu         sync.Mutex
	latest     string
	notesURL   string
	checkedAt  time.Time
	nextTry    time.Time
	refreshing bool
	done       chan struct{} // closed after each refresh; tests only
}

func newUpdateChecker(url, current string) *updateChecker {
	if url == "" {
		url = defaultUpdateManifestURL
	}
	// The default transport honors HTTPS_PROXY / NO_PROXY.
	return &updateChecker{url: url, current: current, client: &http.Client{Timeout: updateFetchTimeout}, now: time.Now}
}

func (c *updateChecker) snapshot() updateNotice {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := updateNotice{Current: c.current, Latest: c.latest, NotesURL: c.notesURL, AutoUpdate: autoUpdateMode()}
	if !c.checkedAt.IsZero() {
		n.CheckedAt = c.checkedAt.UTC().Format(time.RFC3339)
	}
	n.UpdateAvailable = c.latest != "" && releaseIsNewer(c.latest, c.current)
	if !c.now().Before(c.nextTry) && !c.refreshing {
		c.refreshing = true
		go c.refresh()
	}
	return n
}

func (c *updateChecker) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), updateFetchTimeout)
	defer cancel()
	latest, notes, err := c.fetch(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshing = false
	if err != nil {
		c.nextTry = c.now().Add(updateRetryAfter)
	} else {
		c.latest, c.notesURL, c.checkedAt = latest, notes, c.now()
		c.nextTry = c.now().Add(updateRefreshAfter)
	}
	if c.done != nil {
		close(c.done)
		c.done = nil
	}
}

func (c *updateChecker) fetch(ctx context.Context) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/json")
	res, err := c.client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", "", io.ErrUnexpectedEOF
	}
	var m struct {
		Version  string `json:"version"`
		NotesURL string `json:"notes_url"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, updateManifestLimit)).Decode(&m); err != nil {
		return "", "", err
	}
	if _, ok := releaseVersionKey(m.Version); !ok {
		return "", "", io.ErrUnexpectedEOF
	}
	notes := m.NotesURL
	if !strings.HasPrefix(notes, "https://") {
		notes = ""
	}
	return m.Version, notes, nil
}

func autoUpdateMode() string {
	if os.Getenv("TOFI_AUTO_UPDATE") == "patch" {
		return "patch"
	}
	return "off"
}

var (
	sharedUpdateMu      sync.Mutex
	sharedUpdateChecker *updateChecker
)

func sharedUpdates() *updateChecker {
	sharedUpdateMu.Lock()
	defer sharedUpdateMu.Unlock()
	if sharedUpdateChecker == nil {
		sharedUpdateChecker = newUpdateChecker(os.Getenv("TOFI_UPDATE_MANIFEST_URL"), Version)
	}
	return sharedUpdateChecker
}

// serveSystemUpdate answers GET /api/system/update. Callers enforce admin.
func serveSystemUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, sharedUpdates().snapshot())
}
