package main

import (
	"testing"
	"time"
)

func TestProbeCannotExpandInputOrCreateAuthState(t *testing.T) {
	for _, fixture := range []string{"real-user-data", "public_search"} {
		got, code := probe(t.TempDir(), fixture, time.Second)
		if code != 2 || got.Attempts != 0 || got.ExecutionApproved || got.Availability != "not_tested" {
			t.Fatalf("unsafe probe=%+v code=%d", got, code)
		}
	}
	if schemaToken("token=synthetic-secret") != "" || schemaToken("model_not_found") != "model_not_found" {
		t.Fatal("unsafe error metadata")
	}
}
