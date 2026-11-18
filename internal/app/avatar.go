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

// avatarGrounds are the colours a Bot's ground may take (schemaVersion 2). The UI list is GROUNDS in ui/src/avatarStore.ts.
var avatarGrounds = []string{"tomato", "mustard", "teal", "grape", "lime"}

var errAvatarInvalid = errors.New("avatar must be a JSON object with schemaVersion 1 (shape, pattern, palette) or 2 (the same plus ground: tomato, mustard, teal, grape or lime), from the avatar picker")

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
		Ground        string `json:"ground"`
	}
	if err := dec.Decode(&in); err != nil {
		return "", errAvatarInvalid
	}
	if in.SchemaVersion == 0 {
		in.SchemaVersion = 1
	}
	cat := avatarOptions()
	palettes, ok := cat.Patterns[in.Pattern]
	if !ok || !inStrings(cat.Shapes, in.Shape) || !inStrings(palettes, in.Palette) {
		return "", errAvatarInvalid
	}
	look := map[string]any{"schemaVersion": in.SchemaVersion, "shape": in.Shape, "pattern": in.Pattern, "palette": in.Palette}
	switch in.SchemaVersion {
	case 1:
		// A version 1 look has no ground; a ground key on it is refused.
		if in.Ground != "" {
			return "", errAvatarInvalid
		}
	case 2:
		if !inStrings(avatarGrounds, in.Ground) {
			return "", errAvatarInvalid
		}
		look["ground"] = in.Ground
	default:
		return "", errAvatarInvalid
	}
	out, _ := json.Marshal(look)
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
	fmt.Fprintf(&b, "A JSON object {\"schemaVersion\":1,\"shape\":...,\"pattern\":...,\"palette\":...}, or with schemaVersion 2 also \"ground\" (%s). A version 1 write keeps the ground already stored. Shapes: %s. Patterns and their palettes:", strings.Join(avatarGrounds, ", "), strings.Join(cat.Shapes, ", "))
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
	avatar = keepGround(current, avatar)
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

// keepGround carries a stored ground into a version 1 look, which cannot name one: an older client that writes a look must not wipe the
// colour. The result is then a version 2 look. Clearing ("") and a look with its own ground pass through.
func keepGround(current, next string) string {
	if next == "" || current == "" {
		return next
	}
	var held, incoming struct {
		SchemaVersion int    `json:"schemaVersion"`
		Ground        string `json:"ground"`
	}
	if json.Unmarshal([]byte(current), &held) != nil || json.Unmarshal([]byte(next), &incoming) != nil {
		return next
	}
	if held.Ground == "" || incoming.SchemaVersion != 1 {
		return next
	}
	var look map[string]any
	if json.Unmarshal([]byte(next), &look) != nil {
		return next
	}
	look["schemaVersion"] = 2
	look["ground"] = held.Ground
	out, err := json.Marshal(look)
	if err != nil {
		return next
	}
	return string(out)
}

func inStrings(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
