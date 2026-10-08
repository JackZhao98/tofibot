package computer

import (
	"context"
	"github.com/google/uuid"
	"io"
	"net/http"
	"strings"
	"testing"
)

type stagedBlobTransport func(*http.Request) (*http.Response, error)

func (f stagedBlobTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestClientStagedBlobRequiresDurableGuestProtocol(t *testing.T) {
	for _, durable := range []bool{false, true} {
		t.Run(map[bool]string{false: "older_guest", true: "durable_guest"}[durable], func(t *testing.T) {
			id := uuid.NewString()
			digest := strings.Repeat("a", 64)
			calls := 0
			transport := stagedBlobTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "tofi-computer" || r.URL.RawQuery != "" {
					t.Fatal("alternate URL or query")
				}
				response := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}
				if r.URL.Path == "/v1/info" {
					response.Body = io.NopCloser(strings.NewReader(`{"state":"ready"}`))
					return response, nil
				}
				calls++
				if r.URL.Path != "/v1/blobs/"+id {
					t.Fatal("alternate blob path")
				}
				if r.Method == "DELETE" && r.Header.Get("X-Tofi-Staged-SHA256") != digest {
					t.Fatal("cleanup lost object identity")
				}
				if durable {
					response.Header.Set("X-Tofi-Blob-Durability", "1")
				}
				return response, nil
			})
			client, err := New(Config{Socket: "/tmp/synthetic-portability.sock", Client: &http.Client{Transport: transport}})
			if err != nil {
				t.Fatal(err)
			}
			if err = client.PutStagedBlob(context.Background(), id, []byte("synthetic")); (err == nil) != durable {
				t.Fatal("staged write accepted without durable acknowledgement")
			}
			if err = client.DeleteStagedBlob(context.Background(), id, digest); (err == nil) != durable {
				t.Fatal("cleanup accepted without durable acknowledgement")
			}
			if err = client.DeleteStagedBlob(context.Background(), id, "../../outside"); err == nil || calls != 2 {
				t.Fatal("invalid digest reached transport")
			}
		})
	}
}
