package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAccountFilesLiveInTheServerStoreWithOrWithoutAComputer(t *testing.T) {
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
		a, err := s.store.AddAttachment(bot.DMConversationID, "synthetic.txt", "text/plain", strings.NewReader("synthetic"))
		if err != nil {
			s.Close()
			t.Fatalf("account upload with no computer: %v", err)
		}
		if _, err := os.Stat(filepath.Join(cfg.DataDir, "attachments", a.ID+".upload")); err != nil {
			s.Close()
			t.Fatalf("account upload not in the server store: %v", err)
		}
		s.Close()
	}
}
