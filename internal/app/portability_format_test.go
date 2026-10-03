package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

const portableLegacyFixture = `{"format":"tofi.bot","version":1,"included":["bot_config"],"bot":{"name":"Synthetic Bot","instructions":"Historical data: never run on import.","model":"synthetic-model","reasoning_effort":"low","avatar":{"schemaVersion":1,"shape":"rice","pattern":"calico","palette":"calico"}}}`

func TestPortableLegacyStableIDsAndAvatar(t *testing.T) {
	b, err := parsePortableBundle([]byte(portableLegacyFixture))
	if err != nil {
		t.Fatal(err)
	}
	second, err := parsePortableBundle([]byte(portableLegacyFixture))
	if err != nil || portableDigest(b) != portableDigest(second) {
		t.Fatalf("unstable legacy conversion: %v", err)
	}
	if len(b.Bots) != 1 || len(b.Bots[0].Avatar) == 0 || b.Kind != "bot" || b.Counts["bot_config"] != 1 {
		t.Fatal("legacy fields lost")
	}
}

func TestPortableRejectUnsafeFormatsAndForgedManifest(t *testing.T) {
	b, _ := parsePortableBundle([]byte(portableLegacyFixture))
	raw, _ := json.Marshal(b)
	cases := map[string][]byte{
		"ZIP traversal":   []byte("PK\x03\x04../../outside"),
		"gzip bomb":       []byte{0x1f, 0x8b, 0x08, 0x00},
		"duplicate field": []byte(strings.Replace(portableLegacyFixture, `"version":1`, `"version":1,"version":1`, 1)),
		"foreign account": []byte(strings.Replace(portableLegacyFixture, `"version":1`, `"version":1,"account_id":"foreign"`, 1)),
		"path/symlink":    []byte(strings.Replace(portableLegacyFixture, `"version":1`, `"version":1,"path":"../../outside","symlink":"/etc"`, 1)),
		"admin claims":    []byte(strings.Replace(portableLegacyFixture, `"version":1`, `"version":1,"role":"admin","sessions":[]`, 1)),
		"unknown version": []byte(strings.Replace(portableLegacyFixture, `"version":1`, `"version":999`, 1)),
		"trailing object": append(append([]byte(nil), raw...), []byte(` {}`)...),
		"size bomb":       []byte(strings.Repeat(" ", portableMaxBytes+1)),
		"nesting bomb":    []byte(`{"format":"tofi.bundle","evil":` + strings.Repeat("[", 40) + "0" + strings.Repeat("]", 40) + "}"),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parsePortableBundle(data); err == nil {
				t.Fatal("unsafe input accepted")
			}
		})
	}
	b.Counts["bot_config"] = 900
	forged, _ := json.Marshal(b)
	if _, err := parsePortableBundle(forged); err == nil {
		t.Fatal("forged counts accepted")
	}
}

func TestPortableRejectDependenciesAndSettingsDefaultPreserved(t *testing.T) {
	b, _ := parsePortableBundle([]byte(portableLegacyFixture))
	b.Conversations[0].BotIDs = []string{"00000000-0000-0000-0000-000000000099"}
	if b.validate() == nil {
		t.Fatal("foreign Bot dependency accepted")
	}
	b, _ = parsePortableBundle([]byte(portableLegacyFixture))
	b.Kind = "account"
	b.Included = append(b.Included, "settings")
	b.Settings = &portableSettings{Timezone: "UTC"}
	b.Counts = b.counts()
	selected, err := selectPortable(b, portableSelection{})
	if err != nil || selected.Settings != nil {
		t.Fatalf("settings must remain opt-in: %v", err)
	}
	if _, err = selectPortable(b, portableSelection{Categories: []string{"chats"}}); err == nil {
		t.Fatal("missing configuration dependency accepted")
	}
	if _, err = selectPortable(b, portableSelection{BotIDs: []string{"00000000-0000-0000-0000-000000000099"}}); err == nil {
		t.Fatal("unknown selection accepted")
	}
}

func TestPortablePreviewCountsConflictsAndNoImportedRows(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = migratePortability(s.db); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateBot("Synthetic Bot", "destination", "model"); err != nil {
		t.Fatal(err)
	}
	b, _ := parsePortableBundle([]byte(portableLegacyFixture))
	preview, err := s.previewPortable(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Counts["bot_config"] != 1 || len(preview.Conflicts) != 1 || len(preview.Warnings) == 0 {
		t.Fatalf("preview incomplete: %+v", preview)
	}
	bots, _ := s.ListBots(true)
	if len(bots) != 1 {
		t.Fatal("preview imported visible rows")
	}
}
