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
	parent := filepath.Dir(target)
	for {
		info, statErr := os.Stat(parent)
		if statErr == nil {
			if !info.IsDir() {
				return nil, fmt.Errorf("file parent must be a directory")
			}
			break
		}
		if !os.IsNotExist(statErr) {
			return nil, statErr
		}
		parent = filepath.Dir(parent)
	}
	parent, err = filepath.EvalSymlinks(parent)
	if err != nil || !within(root, parent) {
		return nil, fmt.Errorf("file parent escapes guest workspace")
	}
	parentInfo, err := os.Stat(parent)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"target": target, "parent": parent, "parent_object": fileObject(parentInfo), "guard_version": 1}
	if info, err := os.Stat(target); err == nil {
		result["object"] = fileObject(info)
	}
	return result, nil
}

func fileObject(info os.FileInfo) string {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino)
	}
	return ""
}
