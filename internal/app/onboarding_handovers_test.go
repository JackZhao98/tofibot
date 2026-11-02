package app

import (
	"encoding/json"
	"github.com/JackZhao98/tofibot/internal/provider"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func handoverIDs(tasks []trustTask) string {
	ids := make([]string, len(tasks))
	for i, task := range tasks {
		ids[i] = task.ID
	}
	return strings.Join(ids, ",")
}

func TestFilterHandoversFollowsWhatTheInstallCanDo(t *testing.T) {
	tasks := []trustTask{
		{ID: "needs_computer", Tier: "safe", Passes: 4, Runs: 4, Needs: []string{"computer"}},
		{ID: "needs_mail", Tier: "safe", Passes: 4, Runs: 4, Needs: []string{"gmail"}},
		{ID: "needs_nothing", Tier: "safe", Passes: 3, Runs: 3},
		{ID: "glance_task", Tier: "glance", Passes: 4, Runs: 4},
		{ID: "not_for_model", Tier: "safe", Passes: 4, Runs: 4, NotFor: []string{"weak-model"}},
		{ID: "no_runs", Tier: "safe"},
	}
	full := handoverCapabilities{Computer: true, Needs: map[string]bool{"gmail": true}}
	if got := handoverIDs(filterHandovers(tasks, full, "good-model")); got != "needs_computer,needs_mail,not_for_model,needs_nothing" {
		t.Fatalf("full install = %s", got)
	}
	if got := handoverIDs(filterHandovers(tasks, handoverCapabilities{Needs: map[string]bool{"gmail": true}}, "good-model")); strings.Contains(got, "needs_computer") || !strings.Contains(got, "needs_mail") {
		t.Fatalf("no computer = %s", got)
	}
	if got := handoverIDs(filterHandovers(tasks, handoverCapabilities{Computer: true}, "good-model")); strings.Contains(got, "needs_mail") || !strings.Contains(got, "needs_computer") {
		t.Fatalf("no Gmail = %s", got)
	}
	if got := handoverIDs(filterHandovers(tasks, full, "weak-model")); strings.Contains(got, "not_for_model") {
		t.Fatalf("model in not_for = %s", got)
	}
	if got := handoverIDs(filterHandovers(tasks, full, "good-model")); strings.Contains(got, "glance_task") || strings.Contains(got, "no_runs") {
		t.Fatalf("only safe tasks with runs belong: %s", got)
	}
}

func TestFilterHandoversCapsAtFourBestEvidenceFirst(t *testing.T) {
	var tasks []trustTask
	for i, passes := range []int{3, 4, 4, 4, 4, 4} {
		tasks = append(tasks, trustTask{ID: string(rune('a' + i)), Tier: "safe", Passes: passes, Runs: passes})
	}
	if got := handoverIDs(filterHandovers(tasks, handoverCapabilities{}, "")); got != "b,c,d,e" {
		t.Fatalf("order = %s", got)
	}
}

func TestEmbeddedTrustTiersAreWellFormed(t *testing.T) {
	tasks := loadTrustTasks()
	if len(tasks) == 0 {
		t.Fatal("embedded trust report is empty")
	}
	seen := map[string]bool{}
	for _, task := range tasks {
		if task.ID == "" || seen[task.ID] || task.Passes > task.Runs {
			t.Fatalf("bad row %+v", task)
		}
		seen[task.ID] = true
		switch task.Tier {
		case "safe", "glance", "hold", "insufficient":
		default:
			t.Fatalf("unknown tier in %+v", task)
		}
	}
}

func getHandovers(t *testing.T, s *Server) (int, []string) {
	t.Helper()
	rec := providerRequest(t, s, http.MethodGet, "/api/onboarding/handovers", "")
	var out struct {
		Handovers []struct {
			ID string `json:"id"`
		} `json:"handovers"`
	}
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
	}
	ids := []string{}
	for _, h := range out.Handovers {
		ids = append(ids, h.ID)
	}
	return rec.Code, ids
}

func TestOnboardingHandoversEndpoint(t *testing.T) {
	s, err := NewServer(Config{DataDir: t.TempDir(), DefaultModel: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if code, ids := getHandovers(t, s); code != http.StatusOK || len(ids) != 0 {
		t.Fatalf("no Bot: code=%d ids=%v", code, ids)
	}
	b, _, err := s.store.CreateOnboardingBot(uuid.NewString(), "test-model")
	if err != nil {
		t.Fatal(err)
	}
	code, ids := getHandovers(t, s)
	joined := strings.Join(ids, ",")
	if code != http.StatusOK || len(ids) == 0 || len(ids) > maxHandovers {
		t.Fatalf("pending: code=%d ids=%v", code, ids)
	}
	if strings.Contains(joined, "computer_script") || strings.Contains(joined, "printable_checklist") || strings.Contains(joined, "inbox_digest") {
		t.Fatalf("no computer and no Gmail, yet got %s", joined)
	}
	s.microVM = newFakeComputer("firecracker")
	if _, ids = getHandovers(t, s); !strings.Contains(strings.Join(ids, ","), "computer_script") {
		t.Fatalf("with a computer: %v", ids)
	}
	if rec := providerRequest(t, s, http.MethodPost, "/api/onboarding/handovers", "{}"); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d", rec.Code)
	}
	if _, err = s.store.db.Exec(`UPDATE bot_onboarding SET status='completed' WHERE bot_id=?`, b.ID); err != nil {
		t.Fatal(err)
	}
	if code, ids = getHandovers(t, s); code != http.StatusOK || len(ids) != 0 {
		t.Fatalf("completed: code=%d ids=%v", code, ids)
	}
}

func TestOnboardingHandoversSayWhetherTheModelWasEvaluated(t *testing.T) {
	for _, tc := range []struct {
		model     string
		evaluated bool
	}{{"test-model", true}, {provider.CompatModelPrefix + "qwen2.5:7b", false}} {
		s, err := NewServer(Config{DataDir: t.TempDir(), DefaultModel: tc.model})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.store.CreateOnboardingBot(uuid.NewString(), tc.model); err != nil {
			t.Fatal(err)
		}
		rec := providerRequest(t, s, http.MethodGet, "/api/onboarding/handovers", "")
		var out struct {
			Evaluated *bool `json:"evaluated"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Evaluated == nil || *out.Evaluated != tc.evaluated {
			t.Fatalf("%s: evaluated=%v err=%v body=%s", tc.model, out.Evaluated, err, rec.Body.String())
		}
		s.Close()
	}
}
