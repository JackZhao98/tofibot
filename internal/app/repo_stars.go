package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GitHub star counts for the connections the directory links to. The server makes the one anonymous, public read
// (GET /repos/{owner}/{repo}, `stargazers_count`); the browser never calls GitHub and no user data is sent. A count
// is cached for 24 hours and fetched at most once per repository per TTL; a failed fetch is remembered for an hour
// so a rate limit or an outage is not hammered. When there is no cached count the answer simply has no number for
// that repository: no baked-in or invented value.
const (
	repoStarsTTL     = 24 * time.Hour
	repoStarsFailTTL = time.Hour
	repoStarsMax     = 24
)

var repoName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,39}/[A-Za-z0-9_.-]{1,100}$`)

type repoStarEntry struct {
	count    int
	at       time.Time
	failedAt time.Time
}

type repoStarCache struct {
	mu           sync.Mutex
	entries      map[string]repoStarEntry
	inflight     map[string]bool
	blockedUntil time.Time
	baseURL      string // tests point this at a fake GitHub
	client       *http.Client
	clock        func() time.Time
}

func (c *repoStarCache) now() time.Time {
	if c.clock != nil {
		return c.clock()
	}
	return time.Now()
}

func (c *repoStarCache) httpClient() *http.Client {
	if c.client != nil {
		return c.client
	}
	return &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		// A renamed repository redirects within the API; nothing else is followed.
		if len(via) > 3 || req.URL.Scheme != "https" || req.URL.Host != "api.github.com" {
			return http.ErrUseLastResponse
		}
		return nil
	}}
}

// stale returns the repositories that need a fetch now (not fresh, not recently failed, not already being fetched)
// and marks them in flight.
func (c *repoStarCache) stale(repos []string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries, c.inflight = map[string]repoStarEntry{}, map[string]bool{}
	}
	now := c.now()
	if now.Before(c.blockedUntil) {
		return nil
	}
	var need []string
	for _, repo := range repos {
		e, ok := c.entries[repo]
		fresh := ok && e.count >= 0 && now.Sub(e.at) < repoStarsTTL
		failed := ok && !e.failedAt.IsZero() && now.Sub(e.failedAt) < repoStarsFailTTL
		if !fresh && !failed && !c.inflight[repo] {
			c.inflight[repo] = true
			need = append(need, repo)
		}
	}
	return need
}

func (c *repoStarCache) fetch(ctx context.Context, repo string) {
	count, blockedUntil, err := c.read(ctx, repo)
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.inflight, repo)
	prev := c.entries[repo]
	if err != nil {
		// Keep the last good count (it ages out of the answer at the TTL); only the retry time moves.
		prev.failedAt = c.now()
		c.entries[repo] = prev
		if !blockedUntil.IsZero() {
			c.blockedUntil = blockedUntil
		}
		return
	}
	c.entries[repo] = repoStarEntry{count: count, at: c.now()}
}

// read is the anonymous public read. It returns the count, or an error and, when GitHub says the unauthenticated
// limit is used up, the time to wait until.
func (c *repoStarCache) read(ctx context.Context, repo string) (int, time.Time, error) {
	base := c.baseURL
	if base == "" {
		base = "https://api.github.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/repos/"+repo, nil)
	if err != nil {
		return 0, time.Time{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "tofi")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return 0, time.Time{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var until time.Time
		if (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests) && resp.Header.Get("X-RateLimit-Remaining") == "0" {
			if reset, perr := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); perr == nil {
				until = time.Unix(reset, 0)
			}
			if until.IsZero() || until.After(c.now().Add(2*time.Hour)) {
				until = c.now().Add(time.Hour)
			}
		}
		return 0, until, context.DeadlineExceeded
	}
	var body struct {
		Stars *int `json:"stargazers_count"`
	}
	if json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&body) != nil || body.Stars == nil || *body.Stars < 0 {
		return 0, time.Time{}, context.DeadlineExceeded
	}
	return *body.Stars, time.Time{}, nil
}

// counts refreshes what is stale (in parallel, within one deadline) and returns every repository that has a cached,
// unexpired count.
func (c *repoStarCache) counts(ctx context.Context, repos []string) map[string]int {
	need := c.stale(repos)
	if len(need) > 0 {
		ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
		var wg sync.WaitGroup
		for _, repo := range need {
			wg.Add(1)
			go func(repo string) { defer wg.Done(); c.fetch(ctx, repo) }(repo)
		}
		wg.Wait()
		cancel()
	}
	out := map[string]int{}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for _, repo := range repos {
		if e, ok := c.entries[repo]; ok && e.count >= 0 && !e.at.IsZero() && now.Sub(e.at) < repoStarsTTL {
			out[repo] = e.count
		}
	}
	return out
}

// routeRepoStars answers GET /api/extensions/repo-stars?repos=owner/name,owner/name with {"stars":{"owner/name":N}};
// a repository with no known count is left out.
func (s *Server) routeRepoStars(w http.ResponseWriter, r *http.Request, p string) bool {
	if p != "extensions/repo-stars" || r.Method != http.MethodGet {
		return false
	}
	seen := map[string]bool{}
	repos := []string{}
	for _, name := range strings.Split(r.URL.Query().Get("repos"), ",") {
		name, _ = url.PathUnescape(strings.TrimSpace(name))
		if repoName.MatchString(name) && !seen[name] && len(repos) < repoStarsMax {
			seen[name] = true
			repos = append(repos, name)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"stars": s.repoStars.counts(r.Context(), repos)})
	return true
}
