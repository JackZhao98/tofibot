package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

type metadataFixtureEngine struct{}

func (metadataFixtureEngine) Run(context.Context, runtime.Request) (runtime.Result, error) {
	return runtime.Result{}, errors.New("Synthetic fixture: no model or external action is permitted")
}

// Opt-in production-component browser acceptance, with a disposable SQLite
// database and a non-network engine. No production route or user data is used.
func TestDisplayMetadataBrowserFixture(t *testing.T) {
	urlFile := os.Getenv("TOFI_METADATA_BROWSER_URL")
	if urlFile == "" {
		t.Skip("opt-in rendered metadata acceptance")
	}
	dir := t.TempDir()
	config := Config{DataDir: dir, Environment: "acceptance", Provider: "test", Engine: metadataFixtureEngine{}, MCPConfigPath: filepath.Join(dir, "mcp.json"), SkillsDir: filepath.Join(dir, "skills"), UIDir: "../../ui/dist"}
	server, err := NewServer(config)
	if err != nil {
		t.Fatal(err)
	}
	bot, _ := server.store.CreateBot("元数据验收", "Use synthetic fixtures only.", "test-model")
	conv, _ := server.store.GetConversation(bot.DMConversationID)
	_, err = server.store.AddMemoryWithMetadata(conv.ID, bot.ID, MemoryInput{Title: "饮品偏好", Description: "保留中文事实和专有名称。", Content: "MEMORY_BODY_SENTINEL\n事实：用户喜欢茶；称呼是小李。東京 / München."})
	if err != nil {
		t.Fatal(err)
	}
	_, err = server.store.AddMemory(conv.ID, bot.ID, "LEGACY_MEMORY_BODY_SENTINEL\n历史事实保持原文。")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(time.Hour)
	for _, kind := range []string{"once", "interval", "daily"} {
		spec := ScheduleSpec{Title: "标题 " + kind, Description: "说明 " + kind + "：简短的用户说明。", Content: "EXECUTION_PROMPT_SENTINEL_" + kind + "\nFollow the complete synthetic English instruction. Preserve the quoted label ‘上海’.", Kind: kind, Timezone: "UTC", CreatedBy: "bot"}
		if kind == "daily" {
			spec.DailyTime = start.UTC().Format("15:04")
		} else {
			spec.RunAt = start.UTC().Format(time.RFC3339Nano)
		}
		if kind == "interval" {
			spec.IntervalSeconds = 3600
		}
		if _, err := server.store.CreateSchedule(conv.ID, bot.ID, spec); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := server.store.ClaimDueSchedules(start.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range runs {
		server.store.SetRunStatus(run.ID, "failed", "Synthetic failure for retry presentation")
	}
	if _, err := server.store.CreateSchedule(conv.ID, bot.ID, ScheduleSpec{Content: "LEGACY_EXECUTION_PROMPT_SENTINEL\nHistorical instructions stay unchanged.", Kind: "once", RunAt: start.Add(3 * time.Hour).Format(time.RFC3339Nano), Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	stop := make(chan struct{})
	var once sync.Once
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		active := server
		mu.Unlock()
		switch r.URL.Path {
		case "/acceptance/context":
			memories, _ := active.store.MemoriesForConversation(conv.ID, conv)
			schedules, _ := active.store.ListSchedules(conv.ID)
			messages, _, _ := active.store.Messages(conv.ID, 0, 100)
			runs, _ := active.store.Runs(conv.ID)
			roots := []string{}
			for _, run := range runs {
				if run.Kind == runKindSchedule && run.ParentRunID == "" {
					roots = append(roots, run.ID)
				}
			}
			occurrences, _ := active.store.ScheduleOccurrences(r.Context(), conv.ID, roots)
			writeJSON(w, 200, map[string]any{"bot": bot, "conversation": conv, "memories": memories, "schedules": schedules, "messages": messages, "runs": runs, "occurrences": occurrences})
		case "/acceptance/restart":
			if r.Method != "POST" {
				http.Error(w, "POST required", 405)
				return
			}
			active.Close()
			replacement, err := NewServer(config)
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			mu.Lock()
			server = replacement
			mu.Unlock()
			writeJSON(w, 200, map[string]bool{"restarted": true})
		case "/acceptance/stop":
			if r.Method != "POST" {
				http.Error(w, "POST required", 405)
				return
			}
			writeJSON(w, 200, map[string]bool{"stopped": true})
			once.Do(func() { close(stop) })
		default:
			active.Handler().ServeHTTP(w, r)
		}
	}))
	defer httpServer.Close()
	defer func() { mu.Lock(); active := server; mu.Unlock(); active.Close() }()
	if err := os.WriteFile(urlFile, []byte(httpServer.URL), 0600); err != nil {
		t.Fatal(err)
	}
	fmt.Printf("metadata browser fixture %s\n", httpServer.URL)
	select {
	case <-stop:
	case <-time.After(12 * time.Minute):
		t.Fatal("browser fixture timeout")
	}
}
