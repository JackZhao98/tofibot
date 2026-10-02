package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAccountFilesCannotFallbackToUnboundedHostAttachments(t *testing.T) {
	for _, cfg := range []Config{{IsolatedWorkspace: true}, {AccountRuntime: true}} {
		cfg.DataDir = t.TempDir()
		s, err := NewServer(cfg)
		if err != nil {
			t.Fatal(err)
		}
		bot, err := s.store.CreateBot("fixture", "", "model")
		if err != nil {
			s.Close()
			t.Fatal(err)
		}
		if _, err := s.store.AddAttachment(bot.DMConversationID, "synthetic.txt", "text/plain", strings.NewReader("synthetic")); err == nil {
			s.Close()
			t.Fatal("account files bypassed cloud quota")
		}
		if _, err := os.Stat(filepath.Join(cfg.DataDir, "attachments")); !os.IsNotExist(err) {
			s.Close()
			t.Fatal("account upload staged on host")
		}
		s.Close()
	}
}
