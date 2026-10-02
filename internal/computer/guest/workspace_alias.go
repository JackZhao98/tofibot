package guest

import (
	"errors"
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
	alias := filepath.Join(aliasRoot, slug+"--"+shortID)
	target, err := filepath.Abs(bot)
	if err != nil {
		return "", err
	}

	s.workspaceAliasMu.Lock()
	defer s.workspaceAliasMu.Unlock()
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

	info, err := os.Lstat(alias)
	if err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return "", errors.New("workspace alias collides with a non-symlink")
		}
		resolved, resolveErr := filepath.EvalSymlinks(alias)
		if resolveErr != nil || filepath.Clean(resolved) != filepath.Clean(target) {
			return "", errors.New("workspace alias points to another workspace")
		}
		return alias, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.Symlink(target, alias); err != nil {
		return "", err
	}
	return alias, nil
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
