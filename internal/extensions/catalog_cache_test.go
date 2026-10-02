package extensions

import (
	"strings"
	"testing"
	"time"
)

func sampleCatalog() []MCPCatalogTool {
	return []MCPCatalogTool{{RemoteName: "lookup", Description: "Lookup an item", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}}}}
}

func TestMCPCatalogCacheGetPutAndDeepCopy(t *testing.T) {
	c := NewMCPCatalogCache(0, 0, time.Minute)
	tools := sampleCatalog()
	if err := c.Put("server-a", "config-a", "schema-1", tools); err != nil {
		t.Fatal(err)
	}
	// Mutating either side of the cache boundary must not affect stored metadata.
	tools[0].InputSchema["type"] = "changed"
	got, ok := c.Get("server-a", "config-a", "schema-1")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got[0].InputSchema["type"] != "object" {
		t.Fatalf("stored schema was mutated: %#v", got[0].InputSchema)
	}
	got[0].InputSchema["type"] = "also changed"
	again, ok := c.Get("server-a", "config-a", "schema-1")
	if !ok || again[0].InputSchema["type"] != "object" {
		t.Fatal("returned schema was not a deep copy")
	}
	if _, ok := c.Get("server-a", "config-b", "schema-1"); ok {
		t.Fatal("config version must partition cache")
	}
	if _, ok := c.Get("server-a", "config-a", "schema-2"); ok {
		t.Fatal("schema version must partition cache")
	}
}

func TestMCPCatalogCacheExpiryAndInvalidation(t *testing.T) {
	c := NewMCPCatalogCache(4, 1<<20, time.Minute)
	now := time.Now()
	c.now = func() time.Time { return now }
	if err := c.Put("server-a", "config-a", "schema-1", sampleCatalog()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if _, ok := c.Get("server-a", "config-a", "schema-1"); ok {
		t.Fatal("entry should expire at TTL boundary")
	}
	if err := c.Put("server-a", "config-a", "schema-1", sampleCatalog()); err != nil {
		t.Fatal(err)
	}
	if err := c.Put("server-a", "config-a", "schema-2", sampleCatalog()); err != nil {
		t.Fatal(err)
	}
	c.InvalidateConfig("server-a", "config-a")
	if _, ok := c.Get("server-a", "config-a", "schema-1"); ok {
		t.Fatal("config invalidation retained schema-1")
	}
	if _, ok := c.Get("server-a", "config-a", "schema-2"); ok {
		t.Fatal("config invalidation retained schema-2")
	}
	if err := c.Put("server-a", "config-a", "schema-1", sampleCatalog()); err != nil {
		t.Fatal(err)
	}
	c.InvalidateServer("server-a")
	if _, ok := c.Get("server-a", "config-a", "schema-1"); ok {
		t.Fatal("server invalidation retained entry")
	}
}

func TestMCPCatalogCacheBoundsAndLRUEviction(t *testing.T) {
	c := NewMCPCatalogCache(2, 1<<20, time.Minute)
	for _, server := range []string{"one", "two"} {
		if err := c.Put(server, "config", "schema", sampleCatalog()); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := c.Get("one", "config", "schema"); !ok {
		t.Fatal("expected hit")
	} // makes one most recent
	if err := c.Put("three", "config", "schema", sampleCatalog()); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("two", "config", "schema"); ok {
		t.Fatal("least-recently-used entry was not evicted")
	}
	if _, ok := c.Get("one", "config", "schema"); !ok {
		t.Fatal("recent entry was evicted")
	}
	tooMany := make([]MCPCatalogTool, maxCatalogToolsPerServer+1)
	if err := c.Put("large", "config", "schema", tooMany); err == nil {
		t.Fatal("expected tool-count rejection")
	}
	oversized := sampleCatalog()
	oversized[0].InputSchema = map[string]any{"blob": strings.Repeat("x", maxCatalogSchemaBytes)}
	if err := c.Put("large", "config", "schema", oversized); err == nil {
		t.Fatal("expected schema-size rejection")
	}
	if c.usedBytes > c.maxBytes || len(c.entries) > c.maxEntries {
		t.Fatal("cache exceeded configured bounds")
	}
}

func TestMCPCatalogCacheRejectsMissingIdentityAndMetadata(t *testing.T) {
	c := NewMCPCatalogCache(0, 0, 0)
	if err := c.Put("", "config", "schema", sampleCatalog()); err == nil {
		t.Fatal("expected missing server rejection")
	}
	bad := sampleCatalog()
	bad[0].InputSchema = nil
	if err := c.Put("server", "config", "schema", bad); err == nil {
		t.Fatal("expected missing schema rejection")
	}
}
