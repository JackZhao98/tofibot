package guest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// fileIdentity performs no mkdir, reads no file content and changes no files.
// Resolve the existing ancestor plus missing suffix so aliases of an existing
// directory cannot disguise a retry of a not-yet-created file.
func (s *Service) fileIdentity(botID, name string) (map[string]any, error) {
	if !botIDPattern.MatchString(botID) {
		return nil, fmt.Errorf("bot_id must be a canonical UUID")
	}
	if strings.TrimSpace(name) == "" {
		name = "."
	}
	p := filepath.Clean(name)
	if !filepath.IsAbs(p) {
		p = filepath.Join(s.root, "bots", botID, p)
	}
	if !within(s.root, p) {
		return nil, fmt.Errorf("path escapes guest workspace")
	}
	root, err := filepath.EvalSymlinks(s.root)
	if err != nil {
		return nil, err
	}
	ancestor := p
	var suffix []string
	for {
		_, err = os.Lstat(ancestor)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return nil, err
		}
		suffix = append(suffix, filepath.Base(ancestor))
		ancestor = parent
	}
	target, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return nil, err
	}
	for i := len(suffix) - 1; i >= 0; i-- {
		target = filepath.Join(target, suffix[i])
	}
	if !within(root, target) {
		return nil, fmt.Errorf("path escapes guest workspace")
	}
	result := map[string]any{"target": target}
	if info, err := os.Stat(target); err == nil {
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			result["object"] = fmt.Sprintf("%d:%d", stat.Dev, stat.Ino)
		}
	}
	return result, nil
}
