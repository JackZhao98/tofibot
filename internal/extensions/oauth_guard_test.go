package extensions

import (
	"context"
	"errors"
	"testing"
)

// A remote MCP server can name a metadata address as its OAuth endpoint; the
// fetch is refused at dial time.
func TestOAuthFetchRefusesMetadataAddress(t *testing.T) {
	var out map[string]any
	_, err := fetchOAuthJSON(context.Background(), "http://169.254.169.254/latest/meta-data", &out)
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("err=%v, want ErrBlockedAddress", err)
	}
}
