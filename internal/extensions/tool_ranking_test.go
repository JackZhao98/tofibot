package extensions

import (
	"fmt"
	"testing"

	"github.com/JackZhao98/tofibot/internal/runtime"
)

func TestToolRankingNaturalLanguageAndExactName(t *testing.T) {
	tools := []runtime.Tool{
		{Name: "send_email", Description: "Send an email message"},
		{Name: "search_encyclopedia", Description: "Search encyclopedia facts about synthetic Quorin"},
		{Name: "get_stock_quote", Description: "Look up current stock price quote by symbol"},
	}
	for _, tc := range []struct{ query, want string }{
		{"Quorin rings color count", "search_encyclopedia"},
		{"get today's stock price for QTEST", "get_stock_quote"},
		{"search_encyclopedia", "search_encyclopedia"},
		{"unmatched vocabulary", ""},
	} {
		got := rankToolMatches(tools, tc.query)
		if tc.want == "" {
			if len(got) != 0 {
				t.Fatalf("unexpected matches: %v", got)
			}
			continue
		}
		if len(got) == 0 || got[0].Name != tc.want {
			t.Fatalf("%q: expected %s", tc.query, tc.want)
		}
	}
}

// These cases define retrieval intent independently from a particular ranking
// implementation. The current lexical baseline handles literal terms; the
// language and synonym cases are intentionally skipped until semantic search
// is introduced, so they can be enabled without changing the fixture.
func TestToolRankingRetrievalQualityFixture(t *testing.T) {
	tools := []runtime.Tool{
		{Name: "weather", Description: "Get current weather conditions by city"},
		{Name: "find_flight", Description: "Search available flights between airports"},
		{Name: "calendar", Description: "Create or list calendar events"},
		{Name: "x", Description: "Fetch a user's recent messages"},
		{Name: "send_email", Description: "Send email to a recipient"},
	}
	for _, tc := range []struct {
		name, query, want string
		semantic          bool
	}{
		{"literal capability", "weather conditions", "weather", false},
		{"Chinese request against English metadata", "今天旧金山天气怎么样", "weather", true},
		{"synonym request", "book an airplane ticket", "find_flight", true},
		{"camelCase-like short tool name", "x", "x", false},
		{"unrelated request", "quasar zeppelin", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.semantic {
				t.Skip("semantic retrieval baseline: enable when multilingual/synonym matching is implemented")
			}
			got := rankToolMatches(tools, tc.query)
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("unrelated query returned %q", got[0].Name)
				}
				return
			}
			if len(got) == 0 || got[0].Name != tc.want {
				t.Fatalf("query %q: expected %q first, got %v", tc.query, tc.want, toolNames(got))
			}
		})
	}
}

func TestToolRankingThousandCandidateBenchmarkFixture(t *testing.T) {
	tools := make([]runtime.Tool, 1000)
	for i := range tools {
		tools[i] = runtime.Tool{Name: fmt.Sprintf("tool_%04d", i), Description: fmt.Sprintf("Read record batch %d from an archive", i)}
	}
	tools[777] = runtime.Tool{Name: "flight_lookup", Description: "Search available flights between airports"}
	got := rankToolMatches(tools, "available flights airports")
	if len(got) == 0 || got[0].Name != "flight_lookup" {
		t.Fatalf("target did not rank first among 1000 candidates: %v", toolNames(got[:min(len(got), 5)]))
	}
}

func BenchmarkToolRankingThousandCandidates(b *testing.B) {
	tools := make([]runtime.Tool, 1000)
	for i := range tools {
		tools[i] = runtime.Tool{Name: fmt.Sprintf("tool_%04d", i), Description: fmt.Sprintf("Read record batch %d from an archive", i)}
	}
	tools[777] = runtime.Tool{Name: "flight_lookup", Description: "Search available flights between airports"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rankToolMatches(tools, "available flights airports")
	}
}

func toolNames(tools []runtime.Tool) []string {
	names := make([]string, len(tools))
	for i := range tools {
		names[i] = tools[i].Name
	}
	return names
}
