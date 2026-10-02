package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"
)

// OpenRouterModel represents a model available on OpenRouter.
type OpenRouterModel struct {
	ID            string `json:"id"`   // e.g., "anthropic/claude-sonnet-4"
	Name          string `json:"name"` // e.g., "Claude Sonnet 4"
	ContextLength int    `json:"context_length"`
	Pricing       struct {
		Prompt     string `json:"prompt"`     // cost per token as string, e.g., "0.000003"
		Completion string `json:"completion"` // cost per token as string, e.g., "0.000015"
	} `json:"pricing"`
}

// openRouterCache holds the cached model list.
var openRouterCache struct {
	mu      sync.Mutex
	models  []OpenRouterModel
	fetched time.Time
}

const openRouterCacheTTL = 1 * time.Hour

// FetchOpenRouterModels returns the list of models from OpenRouter, cached for 1 hour.
func FetchOpenRouterModels() ([]OpenRouterModel, error) {
	openRouterCache.mu.Lock()
	defer openRouterCache.mu.Unlock()

	if time.Since(openRouterCache.fetched) < openRouterCacheTTL && len(openRouterCache.models) > 0 {
		return openRouterCache.models, nil
	}

	models, err := fetchOpenRouterModelsFromAPI()
	if err != nil {
		// Return stale cache if available
		if len(openRouterCache.models) > 0 {
			return openRouterCache.models, nil
		}
		return nil, err
	}

	openRouterCache.models = models
	openRouterCache.fetched = time.Now()
	return models, nil
}

func fetchOpenRouterModelsFromAPI() ([]OpenRouterModel, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get("https://openrouter.ai/api/v1/models")
	if err != nil {
		return nil, fmt.Errorf("fetch openrouter models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openrouter models API returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []OpenRouterModel `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode openrouter models: %w", err)
	}

	// Sort by name for stable ordering
	sort.Slice(result.Data, func(i, j int) bool {
		return result.Data[i].ID < result.Data[j].ID
	})

	return result.Data, nil
}

// OpenRouterModelEntry is a flat struct for API responses (same shape as handleListModels).
type OpenRouterModelEntry struct {
	Name            string  `json:"name"`
	Provider        string  `json:"provider"`
	ContextWindow   int     `json:"context_window"`
	InputCostPer1M  float64 `json:"input_cost_per_1m"`
	OutputCostPer1M float64 `json:"output_cost_per_1m"`
}

// OpenRouterModelsAsEntries converts OpenRouter models to the standard model entry format.
func OpenRouterModelsAsEntries(models []OpenRouterModel) []OpenRouterModelEntry {
	entries := make([]OpenRouterModelEntry, 0, len(models))
	for _, m := range models {
		// Parse per-token pricing to per-1M
		var promptPer1M, completionPer1M float64
		fmt.Sscanf(m.Pricing.Prompt, "%f", &promptPer1M)
		fmt.Sscanf(m.Pricing.Completion, "%f", &completionPer1M)
		promptPer1M *= 1_000_000
		completionPer1M *= 1_000_000

		entries = append(entries, OpenRouterModelEntry{
			Name:            m.ID,
			Provider:        "openrouter",
			ContextWindow:   m.ContextLength,
			InputCostPer1M:  promptPer1M,
			OutputCostPer1M: completionPer1M,
		})
	}
	return entries
}
