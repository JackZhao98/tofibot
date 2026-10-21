package guest

import (
	"os"
	"path/filepath"
	"sort"
)

// tesseractSystemGlob finds the read-only language data the image ships (the
// English and orientation packs). A variable so a test can point it elsewhere.
var tesseractSystemGlob = "/usr/share/tesseract-ocr/*/tessdata"

// userTessdata returns a per-user tessdata directory in the shared home that
// already holds the shipped data (as links), so the user can add more
// .traineddata files next to it without root; "" when the image has no
// tesseract. A file the user placed under a shipped name is never replaced.
func userTessdata(home string) string {
	matches, _ := filepath.Glob(tesseractSystemGlob)
	if len(matches) == 0 {
		return ""
	}
	sort.Strings(matches)
	system := matches[len(matches)-1]
	entries, err := os.ReadDir(system)
	if err != nil {
		return ""
	}
	dir := filepath.Join(home, ".local", "share", "tessdata")
	if os.MkdirAll(dir, 0775) != nil {
		return ""
	}
	for _, entry := range entries {
		link := filepath.Join(dir, entry.Name())
		if _, err := os.Lstat(link); err == nil {
			continue
		}
		_ = os.Symlink(filepath.Join(system, entry.Name()), link)
	}
	return dir
}
