package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestExportFileOpensInTheExistingImportFlow(t *testing.T) {
	g, _, _, _, out := closeAccount(t)
	x := out["export"].(map[string]any)
	pass, link := x["passphrase"].(string), x["link_path"].(string)
	w := accountRequest(g, "GET", link, "", nil)
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="tofi-alice-`) || !strings.HasSuffix(cd, `.tofi"`) {
		t.Fatalf("file name: %s", cd)
	}
	file := w.Body.Bytes()
	// The envelope is the browser's tofi.encrypted v1 with its exact seven fields.
	var env map[string]any
	if json.Unmarshal(file, &env) != nil || len(env) != 7 || env["format"] != "tofi.encrypted" || env["kdf"] != "PBKDF2-SHA256" || env["iterations"] != float64(310000) {
		t.Fatalf("envelope: %v", env)
	}
	// With dashes, without dashes, lower case and padded with spaces all open it.
	plain, err := openExportFile(file, pass)
	if err != nil {
		t.Fatal(err)
	}
	for _, variant := range []string{strings.ReplaceAll(pass, "-", ""), "  " + strings.ToLower(pass) + " "} {
		again, err := openExportFile(file, variant)
		if err != nil || string(again) != string(plain) {
			t.Fatalf("variant %q: %v", variant, err)
		}
	}
	// The decrypted bundle previews through the existing import path.
	bundle, err := parsePortableBundle(plain)
	if err != nil {
		t.Fatal(err)
	}
	g2, _, _ := deleteFixture(t)
	admin2, _ := g2.create(context.Background(), "admin", "", "SyntheticPassword123!", true, accountCreationSecret(t, g2, true))
	ws, err := g2.workspace(admin2)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := ws.store.previewPortable(context.Background(), bundle)
	if err != nil || !preview.CanApply || len(preview.Bots) != 1 || preview.Bots[0].Name != "Synthetic Helper" {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	// Wrong passphrase, altered ciphertext, damaged envelope.
	if _, err := openExportFile(file, "AAAA-BBBB-CCCC-DDDD-EEEE-FFFF-GGGG-HHHH"); err == nil || errors.Is(err, errExportDamaged) {
		t.Fatalf("wrong passphrase: %v", err)
	}
	ct := env["ciphertext"].(string)
	flipped := "A"
	if ct[20:21] == "A" {
		flipped = "B"
	}
	env["ciphertext"] = ct[:20] + flipped + ct[21:]
	altered, _ := json.Marshal(env)
	if _, err := openExportFile(altered, pass); err == nil {
		t.Fatal("altered ciphertext opened")
	}
	env["iterations"] = 1
	structural, _ := json.Marshal(env)
	if _, err := openExportFile(structural, pass); !errors.Is(err, errExportDamaged) {
		t.Fatalf("structural damage: %v", err)
	}
	if _, err := openExportFile([]byte("not json"), pass); !errors.Is(err, errExportDamaged) {
		t.Fatal("garbage accepted")
	}
	// Opening costs one PBKDF2 pass, far below the 3 s budget.
	start := time.Now()
	openExportFile(file, pass)
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("decrypt took %v", d)
	}
}

func TestExportFileName(t *testing.T) {
	cases := map[string]string{"alice": "tofi-alice-2026-10-09.tofi", "Ada Sample": "tofi-Ada-Sample-2026-10-09.tofi", "../x\"y": "tofi-x-y-2026-10-09.tofi", "李雷": "tofi-account-2026-10-09.tofi"}
	for in, want := range cases {
		if got := exportFileName(in, time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC).Unix()); got != want {
			t.Fatalf("%q: %q want %q", in, got, want)
		}
	}
}
