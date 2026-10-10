package extensions

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

const (
	defaultCatalogCacheTTL     = 2 * time.Minute
	defaultCatalogCacheBytes   = 32 << 20
	defaultCatalogCacheEntries = 256
	maxCatalogToolsPerServer   = 1000
	maxCatalogSchemaBytes      = 64 << 10
	maxCatalogToolNameBytes    = 1024
	maxCatalogDescriptionBytes = 8 << 10
	maxCatalogEntryBytes       = 4 << 20
)

// MCPCatalogTool is inert discovery metadata. It deliberately contains no URL,
// credentials, executable callback, or authorization decision.
type MCPCatalogTool struct {
	RemoteName  string         `json:"remote_name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
	// ReadOnlyHint is the untrusted remote readOnlyHint annotation.
	ReadOnlyHint bool `json:"read_only_hint,omitempty"`
}

type catalogCacheEntry struct {
	key, serverKey, configKey string
	expires                   time.Time
	bytes                     int
	tools                     []MCPCatalogTool
}

// MCPCatalogCache is a process-local, bounded LRU cache. Identity inputs are
// hashed before storage, so opaque config tokens and server identifiers are
// never retained verbatim. Callers must still perform authorization on every
// use; a cache hit is only a source of metadata.
type MCPCatalogCache struct {
	mu         sync.Mutex
	entries    map[string]*list.Element
	lru        *list.List
	maxEntries int
	maxBytes   int
	usedBytes  int
	ttl        time.Duration
	now        func() time.Time
}

// NewMCPCatalogCache creates an in-memory catalog cache. Non-positive limits
// select conservative defaults; TTL is clamped to at most ten minutes.
func NewMCPCatalogCache(maxEntries, maxBytes int, ttl time.Duration) *MCPCatalogCache {
	if maxEntries <= 0 {
		maxEntries = defaultCatalogCacheEntries
	}
	if maxEntries > defaultCatalogCacheEntries {
		maxEntries = defaultCatalogCacheEntries
	}
	if maxBytes <= 0 {
		maxBytes = defaultCatalogCacheBytes
	}
	if maxBytes > defaultCatalogCacheBytes {
		maxBytes = defaultCatalogCacheBytes
	}
	if ttl <= 0 {
		ttl = defaultCatalogCacheTTL
	}
	if ttl > 10*time.Minute {
		ttl = 10 * time.Minute
	}
	return &MCPCatalogCache{entries: map[string]*list.Element{}, lru: list.New(), maxEntries: maxEntries, maxBytes: maxBytes, ttl: ttl, now: time.Now}
}

// Get returns a deep copy for the exact server/config/schema-version tuple.
func (c *MCPCatalogCache) Get(server, configVersion, schemaVersion string) ([]MCPCatalogTool, bool) {
	if c == nil || server == "" || configVersion == "" || schemaVersion == "" {
		return nil, false
	}
	k := catalogCacheKey(server, configVersion, schemaVersion)
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[k]
	if e == nil {
		return nil, false
	}
	entry := e.Value.(*catalogCacheEntry)
	if !c.now().Before(entry.expires) {
		c.remove(e)
		return nil, false
	}
	c.lru.MoveToFront(e)
	return cloneCatalogTools(entry.tools), true
}

// Put stores bounded metadata. Any invalid/oversized catalog is rejected as a
// whole so callers never mistake a partial result for a complete catalog.
func (c *MCPCatalogCache) Put(server, configVersion, schemaVersion string, tools []MCPCatalogTool) error {
	if c == nil || server == "" || configVersion == "" || schemaVersion == "" {
		return errors.New("server, config version, and schema version are required")
	}
	if len(tools) > maxCatalogToolsPerServer {
		return errors.New("catalog exceeds 1000 tools")
	}
	copyTools := make([]MCPCatalogTool, len(tools))
	encoded, err := json.Marshal(tools)
	if err != nil {
		return errors.New("catalog metadata is not JSON encodable")
	}
	if len(encoded) > maxCatalogEntryBytes {
		return errors.New("catalog exceeds 4 MiB")
	}
	if err := json.Unmarshal(encoded, &copyTools); err != nil {
		return errors.New("catalog metadata is not JSON encodable")
	}
	for _, tool := range copyTools {
		if tool.RemoteName == "" || len(tool.RemoteName) > maxCatalogToolNameBytes || len(tool.Description) > maxCatalogDescriptionBytes || tool.InputSchema == nil {
			return errors.New("catalog contains invalid tool metadata")
		}
		schema, err := json.Marshal(tool.InputSchema)
		if err != nil || len(schema) > maxCatalogSchemaBytes {
			return errors.New("tool input schema exceeds 64 KiB or is not JSON encodable")
		}
	}
	if len(encoded) > c.maxBytes {
		return errors.New("catalog exceeds cache byte capacity")
	}
	k := catalogCacheKey(server, configVersion, schemaVersion)
	entry := &catalogCacheEntry{key: k, serverKey: hashCatalogPart(server), configKey: hashCatalogPart(configVersion), expires: c.now().Add(c.ttl), bytes: len(encoded), tools: copyTools}
	c.mu.Lock()
	defer c.mu.Unlock()
	if old := c.entries[k]; old != nil {
		c.remove(old)
	}
	for len(c.entries) >= c.maxEntries || c.usedBytes+entry.bytes > c.maxBytes {
		oldest := c.lru.Back()
		if oldest == nil {
			return errors.New("catalog cannot fit in cache")
		}
		c.remove(oldest)
	}
	c.entries[k] = c.lru.PushFront(entry)
	c.usedBytes += entry.bytes
	return nil
}

// InvalidateServer removes every config/schema variant for one server.
func (c *MCPCatalogCache) InvalidateServer(server string) {
	if c == nil || server == "" {
		return
	}
	c.invalidate(func(e *catalogCacheEntry) bool { return e.serverKey == hashCatalogPart(server) })
}

// InvalidateConfig removes all schema versions for one server/config pair.
func (c *MCPCatalogCache) InvalidateConfig(server, configVersion string) {
	if c == nil || server == "" || configVersion == "" {
		return
	}
	sk, ck := hashCatalogPart(server), hashCatalogPart(configVersion)
	c.invalidate(func(e *catalogCacheEntry) bool { return e.serverKey == sk && e.configKey == ck })
}

func (c *MCPCatalogCache) invalidate(match func(*catalogCacheEntry) bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for el := c.lru.Back(); el != nil; {
		prev := el.Prev()
		if match(el.Value.(*catalogCacheEntry)) {
			c.remove(el)
		}
		el = prev
	}
}

func (c *MCPCatalogCache) remove(el *list.Element) {
	e := el.Value.(*catalogCacheEntry)
	delete(c.entries, e.key)
	c.usedBytes -= e.bytes
	c.lru.Remove(el)
}

func cloneCatalogTools(in []MCPCatalogTool) []MCPCatalogTool {
	out := make([]MCPCatalogTool, len(in))
	b, _ := json.Marshal(in)
	_ = json.Unmarshal(b, &out)
	return out
}

func catalogCacheKey(server, configVersion, schemaVersion string) string {
	return hashCatalogPart(server) + ":" + hashCatalogPart(configVersion) + ":" + hashCatalogPart(schemaVersion)
}

func hashCatalogPart(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
