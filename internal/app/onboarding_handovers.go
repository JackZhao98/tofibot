package app

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strings"

	evaldata "github.com/JackZhao98/tofibot/eval"
)

// maxHandovers is the number of suggestions the first direct message shows.
const maxHandovers = 4

// trustTask is one row of the embedded trust report.
type trustTask struct {
	ID      string   `json:"id"`
	Tier    string   `json:"tier"`
	Passes  int      `json:"passes"`
	Runs    int      `json:"runs"`
	Needs   []string `json:"needs"`
	Touches []string `json:"touches"`
	NotFor  []string `json:"not_for"`
}

// handoverCapabilities is what this install can do right now.
type handoverCapabilities struct {
	Computer bool
	// Needs holds the other capability names that are available: schedules,
	// web_search, gmail, or the name of a connected service.
	Needs map[string]bool
}

func (c handoverCapabilities) has(need string) bool {
	if need == "computer" {
		return c.Computer
	}
	return c.Needs[need]
}

func loadTrustTasks() []trustTask {
	var file struct {
		Tasks []trustTask `json:"tasks"`
	}
	if json.Unmarshal(evaldata.TrustTiers, &file) != nil {
		return nil
	}
	return file.Tasks
}

// wilsonLower is the lower bound of the 95% Wilson interval of passes in runs.
func wilsonLower(passes, runs int) float64 {
	if runs <= 0 {
		return 0
	}
	const z = 1.96
	n, p := float64(runs), float64(passes)/float64(runs)
	return (p + z*z/(2*n) - z*math.Sqrt(p*(1-p)/n+z*z/(4*n*n))) / (1 + z*z/n)
}

// filterHandovers keeps the safe tasks whose needs are all available and whose
// not_for list does not name the default model, best evidence first, at most
// maxHandovers. The result carries ids only; the interface owns the wording.
func filterHandovers(tasks []trustTask, caps handoverCapabilities, model string) []trustTask {
	var out []trustTask
	for _, task := range tasks {
		if task.Tier != "safe" || task.Runs <= 0 {
			continue
		}
		ok := true
		for _, need := range task.Needs {
			if !caps.has(need) {
				ok = false
				break
			}
		}
		for _, bad := range task.NotFor {
			if model != "" && strings.EqualFold(bad, model) {
				ok = false
			}
		}
		if ok {
			out = append(out, task)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := wilsonLower(out[i].Passes, out[i].Runs), wilsonLower(out[j].Passes, out[j].Runs)
		if a != b {
			return a > b
		}
		return out[i].Runs > out[j].Runs
	})
	if len(out) > maxHandovers {
		out = out[:maxHandovers]
	}
	return out
}

func (s *Server) handoverCapabilities(ctx context.Context) handoverCapabilities {
	caps := handoverCapabilities{Computer: s.computer() != nil, Needs: map[string]bool{"schedules": true}}
	if s.webSearchEnabled() {
		caps.Needs["web_search"] = true
	}
	if s.extensions != nil {
		if servers, err := s.extensions.ListMCP(); err == nil {
			for _, server := range servers {
				if server.OAuth != nil && server.OAuth.Connected {
					caps.Needs[server.Name] = true
				}
				if server.Name == "local_gog" && s.mcpServerUsable(ctx, "local_gog") {
					caps.Needs["gmail"] = true
				}
			}
		}
	}
	return caps
}

// routeOnboardingHandovers answers GET /api/onboarding/handovers: the proven
// tasks this install can do now, only while the first Bot's setup is pending.
// It makes no model call.
func (s *Server) routeOnboardingHandovers(w http.ResponseWriter, r *http.Request, path string) bool {
	if path != "onboarding/handovers" {
		return false
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "use GET")
		return true
	}
	empty := map[string]any{"handovers": []map[string]any{}}
	primary, err := s.store.PrimaryBotID()
	botID := strings.TrimSpace(r.URL.Query().Get("bot_id"))
	if err != nil || primary == "" || (botID != "" && botID != primary) {
		writeJSON(w, http.StatusOK, empty)
		return true
	}
	if pending, err := s.store.onboardingPending(primary); err != nil || !pending {
		writeJSON(w, http.StatusOK, empty)
		return true
	}
	picked := filterHandovers(loadTrustTasks(), s.handoverCapabilities(r.Context()), s.effectiveDefaultModel(r.Context()))
	items := make([]map[string]any, 0, len(picked))
	for _, task := range picked {
		items = append(items, map[string]any{"id": task.ID, "passes": task.Passes, "runs": task.Runs})
	}
	writeJSON(w, http.StatusOK, map[string]any{"handovers": items})
	return true
}
