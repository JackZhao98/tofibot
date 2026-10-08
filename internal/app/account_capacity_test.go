package app

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

func capacityFixture() accountCapacity {
	return accountCapacity{TotalBytes: 100 << 30, AvailableBytes: 30 << 30, PromisedBytes: 8 << 30,
		UnallocatedPromises: 8 << 30, AdmissionRemaining: 12 << 30,
		Accounts: []accountDiskCapacity{{AccountID: uuid.NewString(), Slot: 23, QuotaBytes: 8 << 30, State: "reserved"}}}
}

func TestAccountCapacityMetricsFailClosed(t *testing.T) {
	good := capacityFixture()
	data, _ := json.Marshal(good)
	if _, err := parseAccountCapacity(data); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*accountCapacity){
		func(c *accountCapacity) { c.AvailableBytes = c.TotalBytes + 1 },
		func(c *accountCapacity) { c.AdmissionRemaining = c.AvailableBytes },
		func(c *accountCapacity) { c.PromisedBytes++ },
		func(c *accountCapacity) { c.ExternalReserved = -1 },
		func(c *accountCapacity) { c.InternalReserved = 1 },
		func(c *accountCapacity) { c.ExternalReserved = 20 << 30; c.SafetyReserved = 20 << 30 },
		func(c *accountCapacity) {
			c.Accounts = append(c.Accounts, c.Accounts[0])
			c.PromisedBytes *= 2
			c.UnallocatedPromises *= 2
		},
		func(c *accountCapacity) { c.Accounts[0].AccountID = "../personal" },
		func(c *accountCapacity) { c.Accounts[0].State = "ready" },
	} {
		bad := capacityFixture()
		change(&bad)
		data, _ := json.Marshal(bad)
		if _, err := parseAccountCapacity(data); err == nil {
			t.Fatalf("accepted malformed metrics %s", data)
		}
	}
	for _, data := range []string{`{}`, `{"total_bytes":100}`, `null`, `[]`, string(data) + `{}`, strings.Replace(string(data), `"warning":false`, `"warning":null`, 1), strings.Replace(string(data), `"logical_bytes":0`, `"logical_bytes":null`, 1)} {
		if _, err := parseAccountCapacity([]byte(data)); err == nil {
			t.Fatal("accepted incomplete metrics")
		}
	}
}

func TestAccountAdminCapacityEndpointCannotLeakOrStartComputer(t *testing.T) {
	g := accountFixture(t)
	admin, err := g.create(context.Background(), "admin", "", "SyntheticPassword123!", true, accountCreationSecret(t, g, true))
	if err != nil {
		t.Fatal(err)
	}
	user, err := g.create(context.Background(), "tenant", "", "SyntheticPassword123!", false, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = g.root.store.db.Exec(`UPDATE accounts SET must_change_password=0 WHERE id=?`, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	ac, uc := accountCookie(t, g, admin), accountCookie(t, g, user)
	root, err := os.MkdirTemp("/tmp", "tofi-cap-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	path := filepath.Join(root, "control.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	var malformed atomic.Bool
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]string
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body["op"] != "capacity" {
			t.Error("capacity request selected an account or lifecycle action")
		}
		if malformed.Load() {
			w.Write([]byte(`{"total_bytes":100}`))
			return
		}
		data, _ := json.Marshal(capacityFixture())
		var obj map[string]any
		json.Unmarshal(data, &obj)
		obj["host_path"] = "synthetic-private-path"
		json.NewEncoder(w).Encode(obj)
	})}
	go server.Serve(listener)
	defer server.Close()
	g.config.AccountProvisionerSocket = path
	for _, check := range []struct {
		cookie *http.Cookie
		want   int
	}{{nil, 401}, {uc, 403}} {
		if got := accountRequest(g, "GET", "/api/admin/capacity", "", check.cookie); got.Code != check.want {
			t.Fatalf("status=%d want=%d", got.Code, check.want)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("unauthorized account reached broker")
	}
	w := accountRequest(g, "GET", "/api/admin/capacity", "", ac)
	if w.Code != 200 || strings.Contains(w.Body.String(), "host_path") {
		t.Fatalf("capacity=%d %s", w.Code, w.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatal("capacity did not use one observation")
	}
	if accountRequest(g, "POST", "/api/admin/capacity", `{}`, ac).Code != 405 || calls.Load() != 1 {
		t.Fatal("capacity endpoint accepted a write")
	}
	malformed.Store(true)
	if accountRequest(g, "GET", "/api/admin/capacity", "", ac).Code != 503 {
		t.Fatal("invented capacity from missing metrics")
	}
	g.config.AccountProvisionerSocket = ""
	if accountRequest(g, "GET", "/api/admin/capacity", "", ac).Code != 503 {
		t.Fatal("unconfigured broker reported zero capacity")
	}
}

func TestAccountCapacityReservationsAreExposed(t *testing.T) {
	c := capacityFixture()
	c.Accounts[0].PendingQuota = true
	c.ExternalReserved = 4 << 30
	c.PerAccountInternalReserved = 1 << 30
	c.InternalReserved = 1 << 30
	c.InternalUnallocated = 1 << 30
	c.SafetyReserved = 5 << 30
	data, _ := json.Marshal(c)
	got, err := parseAccountCapacity(data)
	if err != nil || got.SafetyReserved != c.SafetyReserved || !got.Accounts[0].PendingQuota {
		t.Fatalf("reservations: %+v %v", got, err)
	}
}

func TestAccountCapacityDynamicExternalMeasurements(t *testing.T) {
	good := capacityFixture()
	good.ExternalPromised = 20 << 30
	good.ExternalAllocated = 12 << 30
	good.ExternalUnallocated = 8 << 30
	good.AdmissionRemaining = 12 << 30
	data, _ := json.Marshal(good)
	parsed, err := parseAccountCapacity(data)
	if err != nil || parsed.ExternalPromised != good.ExternalPromised {
		t.Fatalf("metrics=%+v err=%v", parsed, err)
	}
	for _, change := range []func(*accountCapacity){
		func(c *accountCapacity) { c.ExternalAllocated++ },
		func(c *accountCapacity) { c.ExternalPromised = -1 },
		func(c *accountCapacity) { c.AdmissionRemaining = 15 << 30 },
	} {
		bad := good
		change(&bad)
		body, _ := json.Marshal(bad)
		if _, err := parseAccountCapacity(body); err == nil {
			t.Fatal("accepted inconsistent dynamic external metrics")
		}
	}
	var object map[string]json.RawMessage
	json.Unmarshal(data, &object)
	delete(object, "external_allocated_bytes")
	data, _ = json.Marshal(object)
	if _, err := parseAccountCapacity(data); err == nil {
		t.Fatal("accepted partial dynamic metric group")
	}
}

func TestAccountCapacityInternalCopyCredit(t *testing.T) {
	good := capacityFixture()
	good.PerAccountInternalReserved = 8 << 30
	good.InternalReserved = 8 << 30
	good.SafetyReserved = 8 << 30
	good.InternalAllocated = 5 << 30
	good.InternalUnallocated = 3 << 30
	good.AdmissionRemaining = good.AvailableBytes - good.UnallocatedPromises - good.InternalUnallocated
	data, _ := json.Marshal(good)
	if _, err := parseAccountCapacity(data); err != nil {
		t.Fatal(err)
	}
	var legacy map[string]any
	json.Unmarshal(data, &legacy)
	delete(legacy, "internal_allocated_bytes")
	delete(legacy, "internal_unallocated_reserved_bytes")
	legacy["admission_remaining_bytes"] = good.AvailableBytes - good.UnallocatedPromises - good.InternalReserved
	legacyData, _ := json.Marshal(legacy)
	if parsed, err := parseAccountCapacity(legacyData); err != nil || parsed.InternalUnallocated != good.InternalReserved {
		t.Fatalf("legacy internal reserve: %+v %v", parsed, err)
	}
	for _, key := range []string{"internal_allocated_bytes", "internal_unallocated_reserved_bytes"} {
		var fields map[string]any
		json.Unmarshal(data, &fields)
		delete(fields, key)
		bad, _ := json.Marshal(fields)
		if _, err := parseAccountCapacity(bad); err == nil {
			t.Fatal("accepted partial internal metrics")
		}
	}
	good.InternalAllocated++
	bad, _ := json.Marshal(good)
	if _, err := parseAccountCapacity(bad); err == nil {
		t.Fatal("accepted inconsistent internal metrics")
	}
}
