package guest

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, item := range env {
		if strings.HasPrefix(item, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

// ensureWorkspaceAlias keeps a human-readable link beside the shared HOME.
// It returns an empty path (and no error) when no alias could be placed.
// The UUID directory remains canonical; the link is only a navigation aid.
func (s *Service) ensureWorkspaceAlias(botID, botName string) (string, error) {
	bot, err := s.botDir(botID)
	if err != nil {
		return "", err
	}
	aliasRoot := filepath.Join(s.root, "home", "bots")
	if err := os.MkdirAll(aliasRoot, 0770); err != nil {
		return "", err
	}
	shortID := strings.ToLower(botID[:8])
	slug := workspaceAliasSlug(botName)
	if slug == "" {
		slug = "bot"
	}
	base := slug + "--" + shortID
	target, err := filepath.Abs(bot)
	if err != nil {
		return "", err
	}

	s.workspaceAliasMu.Lock()
	defer s.workspaceAliasMu.Unlock()

	// A plain directory or a link to another workspace may already sit on the
	// preferred name. Never touch it: use a deterministic fallback name, or no
	// alias at all. The alias must never block the Bot's computer.
	alias := ""
	for _, name := range []string{base, base + "-link"} {
		path := filepath.Join(aliasRoot, name)
		if aliasUsable(path, target) {
			alias = path
			break
		}
	}
	if alias == "" {
		log.Printf("computer guest: workspace alias for bot %s skipped: %s and %s are taken", shortID, base, base+"-link")
	} else if filepath.Base(alias) != base {
		log.Printf("computer guest: workspace alias %s is taken; using %s", base, filepath.Base(alias))
	}

	entries, err := os.ReadDir(aliasRoot)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink == 0 {
			continue
		}
		path := filepath.Join(aliasRoot, entry.Name())
		resolved, resolveErr := filepath.EvalSymlinks(path)
		if resolveErr == nil && filepath.Clean(resolved) == filepath.Clean(target) && path != alias {
			if err := os.Remove(path); err != nil {
				return "", err
			}
		}
	}
	if alias == "" {
		return "", nil
	}
	if _, err := os.Lstat(alias); err == nil {
		return alias, nil
	}
	if err := os.Symlink(target, alias); err != nil {
		log.Printf("computer guest: workspace alias %s not created: %v", filepath.Base(alias), err)
		return "", nil
	}
	return alias, nil
}

// aliasUsable reports whether path is free or already links to target.
func aliasUsable(path, target string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return os.IsNotExist(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return false
	}
	resolved, err := filepath.EvalSymlinks(path)
	return err == nil && filepath.Clean(resolved) == filepath.Clean(target)
}

func (s *Service) workspaceAlias(botID string) (string, bool) {
	bot, err := s.botDir(botID)
	if err != nil {
		return "", false
	}
	aliasRoot := filepath.Join(s.root, "home", "bots")
	entries, err := os.ReadDir(aliasRoot)
	if err != nil {
		return "", false
	}
	target, err := filepath.Abs(bot)
	if err != nil {
		return "", false
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink == 0 {
			continue
		}
		path := filepath.Join(aliasRoot, entry.Name())
		resolved, resolveErr := filepath.EvalSymlinks(path)
		if resolveErr == nil && filepath.Clean(resolved) == filepath.Clean(target) {
			return path, true
		}
	}
	return "", false
}

func workspaceAliasSlug(name string) string {
	name = strings.TrimSpace(name)
	var b strings.Builder
	separator := false
	for _, r := range name {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-':
			if separator && b.Len() > 0 {
				b.WriteByte('-')
			}
			separator = false
			b.WriteRune(r)
		case unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r):
			if b.Len() > 0 {
				separator = true
			}
		}
	}
	return strings.Trim(b.String(), "-_.")
}
