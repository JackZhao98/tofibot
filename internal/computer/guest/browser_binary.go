package guest

import (
	"os/exec"
	"strings"
	"sync"
)

// browserCandidates lists the shared browser's executable names in order of
// preference. x86_64 images ship Google Chrome. Google publishes no Linux
// arm64 Chrome, so aarch64 images ship Debian's chromium instead.
var browserCandidates = []string{"google-chrome-stable", "chromium", "chromium-browser"}

var (
	browserOnce sync.Once
	browserName string
)

// browserBinary returns the browser executable installed in this guest. When
// none is found it returns the Chrome name, so a missing browser reports the
// historical error text.
func browserBinary() string {
	browserOnce.Do(func() {
		browserName = browserCandidates[0]
		for _, name := range browserCandidates {
			if _, err := exec.LookPath(name); err == nil {
				browserName = name
				return
			}
		}
	})
	return browserName
}

// versionIsBrowser reports whether `--version` output names a supported browser.
func versionIsBrowser(output string) bool {
	return strings.Contains(output, "Google Chrome") || strings.Contains(output, "Chromium")
}

// browserProcessNames are the executable base names (lower case) whose
// processes own a browser profile.
var browserProcessNames = map[string]bool{
	"chrome": true, "google-chrome": true, "google-chrome-stable": true,
	"chromium": true, "chromium-browser": true,
}
