package app

import (
	"bytes"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// A Bot's avatar is a small look (shape, pattern, palette) that used to live in the browser. It is stored on the Bot so it follows the
// person to every device, an export carries it, and the settings tool can change it. The browser keeps a copy only for the first paint.

//go:embed avatar_catalog.json
var avatarCatalogJSON []byte

// avatarCatalog is the set of looks the avatar picker offers. ui/scripts/test-avatar-store.mjs fails when it differs from the UI library
// (ui/src/lib/tofi-avatar), so the server never stores a look the picker cannot draw.
type avatarCatalog struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Shapes        []string            `json:"shapes"`
	Patterns      map[string][]string `json:"patterns"`
}

var (
	avatarCatalogOnce sync.Once
	avatarCatalogData avatarCatalog
)

func avatarOptions() avatarCatalog {
	avatarCatalogOnce.Do(func() { _ = json.Unmarshal(avatarCatalogJSON, &avatarCatalogData) })
	return avatarCatalogData
}

// BotAvatar is the stored look as JSON text. It marshals as the JSON object itself.
type BotAvatar string

func (a BotAvatar) MarshalJSON() ([]byte, error) {
	if a == "" {
		return []byte("null"), nil
	}
	return []byte(a), nil
}

func (a *BotAvatar) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if string(data) == "null" || len(data) == 0 {
		*a = ""
		return nil
	}
	*a = BotAvatar(data)
	return nil
}

var errAvatarInvalid = errors.New("avatar must be a JSON object with schemaVersion 1 and a shape, pattern and palette from the avatar picker")

// normalizeAvatar checks a look and returns it in one canonical form. Empty text clears the stored look. Anything the picker cannot draw is
// refused.
func normalizeAvatar(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return "", nil
	}
	if len(raw) > 512 {
		return "", errAvatarInvalid
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var in struct {
		SchemaVersion int    `json:"schemaVersion"`
		Shape         string `json:"shape"`
		Pattern       string `json:"pattern"`
		Palette       string `json:"palette"`
	}
	if err := dec.Decode(&in); err != nil {
		return "", errAvatarInvalid
	}
	if in.SchemaVersion == 0 {
		in.SchemaVersion = 1
	}
	cat := avatarOptions()
	palettes, ok := cat.Patterns[in.Pattern]
	if in.SchemaVersion != 1 || !ok || !inStrings(cat.Shapes, in.Shape) || !inStrings(palettes, in.Palette) {
		return "", errAvatarInvalid
	}
	out, _ := json.Marshal(map[string]any{"schemaVersion": 1, "shape": in.Shape, "pattern": in.Pattern, "palette": in.Palette})
	return string(out), nil
}

// avatarHelp lists what a Bot may choose, for the settings tool.
func avatarHelp() string {
	cat := avatarOptions()
	patterns := make([]string, 0, len(cat.Patterns))
	for p := range cat.Patterns {
		patterns = append(patterns, p)
	}
	sort.Strings(patterns)
	var b strings.Builder
	fmt.Fprintf(&b, "A JSON object {\"schemaVersion\":1,\"shape\":...,\"pattern\":...,\"palette\":...}. Shapes: %s. Patterns and their palettes:", strings.Join(cat.Shapes, ", "))
	for _, p := range patterns {
		fmt.Fprintf(&b, " %s (%s);", p, strings.Join(cat.Patterns[p], ", "))
	}
	return b.String()
}

func migrateBotAvatar(db *sql.DB) error {
	return ensureColumn(db, "bots", "avatar", `ALTER TABLE bots ADD COLUMN avatar TEXT NOT NULL DEFAULT ''`)
}

// botAvatars returns the stored looks by Bot id; a Bot with none is absent.
func (s *Store) botAvatars() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT id,avatar FROM bots WHERE avatar<>''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, avatar string
		if err := rows.Scan(&id, &avatar); err != nil {
			return nil, err
		}
		out[id] = avatar
	}
	return out, rows.Err()
}

func (s *Store) botAvatar(id string) (string, error) {
	var avatar string
	err := s.db.QueryRow(`SELECT avatar FROM bots WHERE id=?`, id).Scan(&avatar)
	return avatar, err
}

// SetBotAvatar stores a look (already normalized) or clears it with "".
func (s *Store) SetBotAvatar(id, avatar string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current string
	if err := tx.QueryRow(`SELECT avatar FROM bots WHERE id=?`, id).Scan(&current); err != nil {
		return err
	}
	if current == avatar {
		return nil
	}
	if _, err := tx.Exec(`UPDATE bots SET avatar=? WHERE id=?`, avatar, id); err != nil {
		return err
	}
	if err := insertWorkspaceEventTx(tx, workspaceScopeBots, now()); err != nil {
		return err
	}
	return tx.Commit()
}

func inStrings(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
