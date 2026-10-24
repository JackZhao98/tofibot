package app

// Skill library (decision 0058). One global library; each Bot has its own
// enabled set. The files under the skills directory stay the source of truth
// for what a skill says; these tables record who put a skill there, which
// versions exist, and which Bot has it turned on:
//
//   - skill_library: one row per installed skill (installer, current version);
//   - skill_versions: the versions of a skill (files archived under
//     <skills>/.versions/<name>/<n>/);
//   - skill_bundles: a group of skills installed together from a plugin folder;
//   - skill_enablement: (Bot, skill) -> on/off with who set it and when. An
//     installed skill with no row is off; an official (built-in) skill with no
//     row is on, so a new Bot starts with the official skills and nothing else;
//   - skill_library_markers: one-time data migrations.
//
// The old skill_access and builtin_skill_off tables are read once, by the
// one-time migration, and never again. They are kept, not dropped.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/JackZhao98/tofibot/internal/extensions"
	"github.com/JackZhao98/tofibot/internal/tooloutcome"
)

const (
	skillActorOwner  = "owner"
	skillActorBot    = "bot"
	skillActorLegacy = "legacy"

	// skill_enablement.set_by values.
	skillSetByOwner     = "owner"
	skillSetByBot       = "bot"
	skillSetByMigration = "migration"

	skillMigrationMarker = "skill_library_per_bot_v1"
	maxSkillVersions     = 20
)

var (
	errSkillNotInstaller = errors.New("only the Bot that installed a skill can publish its versions")
	errSkillNotFound     = errors.New("skill not found")
	errSkillInBundle     = errors.New("skill belongs to a bundle; remove the bundle")
	errSkillUnchanged    = errors.New("the files are the same as the current version")
)

// migrateLegacySkillTables keeps the two tables the per-Bot library replaced.
// They are read once by migrateSkillLibraryData and never written again.
func migrateLegacySkillTables(db *sql.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS skill_access(
skill_name TEXT NOT NULL,
bot_id TEXT NOT NULL,
PRIMARY KEY(skill_name, bot_id)
)`,
		`CREATE TABLE IF NOT EXISTS builtin_skill_off(
skill_name TEXT PRIMARY KEY,
updated_at TEXT NOT NULL
)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			return err
		}
	}
	return nil
}

func migrateSkillLibrary(db *sql.DB) error {
	if db == nil {
		return errors.New("database is required")
	}
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS skill_bundles(
id TEXT PRIMARY KEY,
description TEXT NOT NULL DEFAULT '',
version TEXT NOT NULL DEFAULT '',
installer_kind TEXT NOT NULL,
installer_bot_id TEXT NOT NULL DEFAULT '',
installed_at TEXT NOT NULL
)`,
		`CREATE TABLE IF NOT EXISTS skill_library(
name TEXT PRIMARY KEY,
installer_kind TEXT NOT NULL,
installer_bot_id TEXT NOT NULL DEFAULT '',
installed_at TEXT NOT NULL,
current_version INTEGER NOT NULL DEFAULT 1,
bundle_id TEXT NOT NULL DEFAULT '',
source TEXT NOT NULL DEFAULT '',
digest TEXT NOT NULL DEFAULT '',
updated_at TEXT NOT NULL
)`,
		`CREATE TABLE IF NOT EXISTS skill_versions(
name TEXT NOT NULL,
version INTEGER NOT NULL,
digest TEXT NOT NULL,
created_by_kind TEXT NOT NULL,
created_by_bot_id TEXT NOT NULL DEFAULT '',
created_at TEXT NOT NULL,
note TEXT NOT NULL DEFAULT '',
PRIMARY KEY(name, version)
)`,
		`CREATE TABLE IF NOT EXISTS skill_enablement(
bot_id TEXT NOT NULL,
skill_name TEXT NOT NULL,
enabled INTEGER NOT NULL,
set_by TEXT NOT NULL,
set_at TEXT NOT NULL,
PRIMARY KEY(bot_id, skill_name)
)`,
		`CREATE TABLE IF NOT EXISTS skill_library_markers(
marker TEXT PRIMARY KEY,
done_at TEXT NOT NULL
)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			return err
		}
	}
	return nil
}

// skillActor says who changes the library: the owner (Settings), a Bot (its
// own tool call) or, for rows found on disk, nobody known.
type skillActor struct {
	Kind  string
	BotID string
}

type skillInstaller struct {
	Kind    string `json:"kind"`
	BotID   string `json:"bot_id,omitempty"`
	BotName string `json:"bot_name,omitempty"`
}

type skillVersionView struct {
	Version   int            `json:"version"`
	CreatedAt string         `json:"created_at"`
	CreatedBy skillInstaller `json:"created_by"`
	Note      string         `json:"note,omitempty"`
	Current   bool           `json:"current"`
}

type librarySkillView struct {
	extensions.SkillView
	BundleID    string             `json:"bundle_id,omitempty"`
	InstalledBy *skillInstaller    `json:"installed_by,omitempty"`
	InstalledAt string             `json:"installed_at,omitempty"`
	Version     int                `json:"version,omitempty"`
	Updated     bool               `json:"updated,omitempty"`
	UpdatedAt   string             `json:"updated_at,omitempty"`
	Versions    []skillVersionView `json:"versions,omitempty"`
	EnabledBots int                `json:"enabled_bots"`
	BotsTotal   int                `json:"bots_total"`
}

type libraryBundleView struct {
	ID          string          `json:"id"`
	Description string          `json:"description,omitempty"`
	Version     string          `json:"version,omitempty"`
	InstalledBy *skillInstaller `json:"installed_by,omitempty"`
	InstalledAt string          `json:"installed_at"`
	Skills      []string        `json:"skills"`
}

type libraryRow struct {
	Name, InstallerKind, InstallerBotID, InstalledAt string
	Version                                          int
	BundleID, Source, Digest, UpdatedAt              string
}

func (s *Store) libraryRows() ([]libraryRow, error) {
	rows, err := s.db.Query(`SELECT name,installer_kind,installer_bot_id,installed_at,current_version,bundle_id,source,digest,updated_at FROM skill_library ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []libraryRow
	for rows.Next() {
		var r libraryRow
		if err := rows.Scan(&r.Name, &r.InstallerKind, &r.InstallerBotID, &r.InstalledAt, &r.Version, &r.BundleID, &r.Source, &r.Digest, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) libraryRow(name string) (libraryRow, bool, error) {
	var r libraryRow
	err := s.db.QueryRow(`SELECT name,installer_kind,installer_bot_id,installed_at,current_version,bundle_id,source,digest,updated_at FROM skill_library WHERE name=?`, name).
		Scan(&r.Name, &r.InstallerKind, &r.InstallerBotID, &r.InstalledAt, &r.Version, &r.BundleID, &r.Source, &r.Digest, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	return r, err == nil, err
}

func (s *Store) botNames() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT id,name FROM bots`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Which skills a Bot has on

// skillEnabledFilter is the extensions.Config.SkillAccess hook. An installed
// skill is on only with an enabled row for this Bot; an official skill is on
// unless this Bot has an off row. Any error is returned so the caller
// withholds every skill rather than granting by default.
func (s *Store) skillEnabledFilter(ctx context.Context, botID string, names []string) (map[string]bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT skill_name, enabled FROM skill_enablement WHERE bot_id=?`, botID)
	if err != nil {
		return nil, err
	}
	state := map[string]bool{}
	for rows.Next() {
		var name string
		var on int
		if err := rows.Scan(&name, &on); err != nil {
			rows.Close()
			return nil, err
		}
		state[name] = on != 0
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	allowed := make(map[string]bool, len(names))
	for _, name := range names {
		on, set := state[name]
		if extensions.IsBuiltinSkill(name) {
			allowed[name] = !set || on
		} else {
			allowed[name] = set && on
		}
	}
	return allowed, nil
}

// setSkillEnabledTx records one Bot's choice for one skill.
func setSkillEnabledTx(tx *sql.Tx, botID, skill string, enabled bool, setBy string) error {
	on := 0
	if enabled {
		on = 1
	}
	_, err := tx.Exec(`INSERT INTO skill_enablement(bot_id,skill_name,enabled,set_by,set_at) VALUES(?,?,?,?,?)
ON CONFLICT(bot_id,skill_name) DO UPDATE SET enabled=excluded.enabled,set_by=excluded.set_by,set_at=excluded.set_at`, botID, skill, on, setBy, now())
	return err
}

// setSkillsEnabled sets several skills for one Bot in one transaction.
func (s *Store) setSkillsEnabled(botID string, skills []string, enabled bool, setBy string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, name := range skills {
		if err = setSkillEnabledTx(tx, botID, name, enabled, setBy); err != nil {
			return err
		}
	}
	if err = insertWorkspaceEventTx(tx, workspaceScopeConfig, now()); err != nil {
		return err
	}
	return tx.Commit()
}

// bundleMembers lists the skills of a bundle.
func (s *Store) bundleMembers(bundle string) ([]string, error) {
	rows, err := s.db.Query(`SELECT name FROM skill_library WHERE bundle_id=? ORDER BY name`, bundle)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// skillTarget resolves a name to one skill (library or official) or to a
// bundle's members. A skill name wins over a bundle of the same name.
func (s *Store) skillTarget(name string) (skills []string, ok bool, err error) {
	if extensions.IsBuiltinSkill(name) {
		return []string{name}, true, nil
	}
	if _, found, err := s.libraryRow(name); err != nil || found {
		return []string{name}, found, err
	}
	members, err := s.bundleMembers(name)
	return members, len(members) > 0, err
}

// ---------------------------------------------------------------------------
// Install, publish, roll back, delete

func (s *Server) actorFromBot(botID string) skillActor {
	return skillActor{Kind: skillActorBot, BotID: botID}
}

func insertVersionTx(tx *sql.Tx, name string, version int, digest string, actor skillActor, note string) error {
	_, err := tx.Exec(`INSERT INTO skill_versions(name,version,digest,created_by_kind,created_by_bot_id,created_at,note) VALUES(?,?,?,?,?,?,?)`,
		name, version, digest, actor.Kind, actor.BotID, now(), note)
	return err
}

func insertLibraryTx(tx *sql.Tx, name string, actor skillActor, bundleID, source, digest string) error {
	t := now()
	if _, err := tx.Exec(`INSERT INTO skill_library(name,installer_kind,installer_bot_id,installed_at,current_version,bundle_id,source,digest,updated_at) VALUES(?,?,?,?,1,?,?,?,?)`,
		name, actor.Kind, actor.BotID, t, bundleID, source, digest, t); err != nil {
		return err
	}
	return insertVersionTx(tx, name, 1, digest, actor, "")
}

// The functions below change the library and its files; the caller publishes
// the one workspace config event (Settings routes through writeExtensionSaved,
// the Bot tool through skillChanged).
//
// installSkill puts a new skill in the library. The owner's install turns it on
// for nobody; a Bot's install turns it on for that Bot only.
//
// enableFor lists Bots the owner chose to turn it on for at install time; the
// rows are written (set_by=owner) in the same transaction as the library row.
func (s *Server) installSkill(actor skillActor, name string, files map[string][]byte, source string, enableFor ...string) error {
	s.skillLibMu.Lock()
	defer s.skillLibMu.Unlock()
	if err := s.extensions.InstallSkill(name, files); err != nil {
		return err
	}
	tx, err := s.store.db.Begin()
	if err != nil {
		_ = s.extensions.DeleteSkill(name)
		return err
	}
	defer tx.Rollback()
	// A row may linger from a skill removed behind our back; the new install replaces it.
	for _, q := range []string{`DELETE FROM skill_library WHERE name=?`, `DELETE FROM skill_versions WHERE name=?`, `DELETE FROM skill_enablement WHERE skill_name=?`} {
		if _, err = tx.Exec(q, name); err != nil {
			_ = s.extensions.DeleteSkill(name)
			return err
		}
	}
	if err = insertLibraryTx(tx, name, actor, "", source, extensions.SkillFilesDigest(files)); err == nil && actor.Kind == skillActorBot {
		err = setSkillEnabledTx(tx, actor.BotID, name, true, skillSetByBot)
	}
	for _, id := range enableFor {
		if err != nil {
			break
		}
		err = setSkillEnabledTx(tx, id, name, true, skillSetByOwner)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		_ = s.extensions.DeleteSkill(name)
		return err
	}
	return nil
}

// installSkillBundle installs every skill of a plugin folder, or none.
// enableFor works as for installSkill and applies to every member.
func (s *Server) installSkillBundle(actor skillActor, bundle extensions.SkillBundle, source string, enableFor ...string) (libraryBundleView, error) {
	s.skillLibMu.Lock()
	defer s.skillLibMu.Unlock()
	var view libraryBundleView
	if _, found, err := s.store.bundleRow(bundle.Name); err != nil {
		return view, err
	} else if found {
		return view, extensions.ErrSkillExists
	}
	if err := s.extensions.InstallSkillBundle(bundle); err != nil {
		return view, err
	}
	cleanup := func() {
		for _, sk := range bundle.Skills {
			_ = s.extensions.DeleteSkill(sk.Name)
		}
	}
	tx, err := s.store.db.Begin()
	if err != nil {
		cleanup()
		return view, err
	}
	defer tx.Rollback()
	t := now()
	if _, err = tx.Exec(`INSERT INTO skill_bundles(id,description,version,installer_kind,installer_bot_id,installed_at) VALUES(?,?,?,?,?,?)`,
		bundle.Name, bundle.Description, bundle.Version, actor.Kind, actor.BotID, t); err != nil {
		cleanup()
		return view, err
	}
	for _, sk := range bundle.Skills {
		for _, q := range []string{`DELETE FROM skill_library WHERE name=?`, `DELETE FROM skill_versions WHERE name=?`, `DELETE FROM skill_enablement WHERE skill_name=?`} {
			if _, err = tx.Exec(q, sk.Name); err != nil {
				cleanup()
				return view, err
			}
		}
		if err = insertLibraryTx(tx, sk.Name, actor, bundle.Name, source, extensions.SkillFilesDigest(sk.Files)); err != nil {
			cleanup()
			return view, err
		}
		for _, id := range enableFor {
			if err = setSkillEnabledTx(tx, id, sk.Name, true, skillSetByOwner); err != nil {
				cleanup()
				return view, err
			}
		}
		view.Skills = append(view.Skills, sk.Name)
	}
	if err = tx.Commit(); err != nil {
		cleanup()
		return view, err
	}
	view.ID, view.Description, view.Version, view.InstalledAt = bundle.Name, bundle.Description, bundle.Version, t
	return view, nil
}

func (s *Store) bundleRow(id string) (libraryBundleView, bool, error) {
	var b libraryBundleView
	var kind, bot string
	err := s.db.QueryRow(`SELECT id,description,version,installer_kind,installer_bot_id,installed_at FROM skill_bundles WHERE id=?`, id).
		Scan(&b.ID, &b.Description, &b.Version, &kind, &bot, &b.InstalledAt)
	if errors.Is(err, sql.ErrNoRows) {
		return b, false, nil
	}
	b.InstalledBy = &skillInstaller{Kind: kind, BotID: bot}
	return b, err == nil, err
}

// skillPublishPreview is what a new version would change. The approval card
// shows it, built from the files and the library, never from the Bot's wording.
type skillPublishPreview struct {
	Version     int
	Added       []string
	Removed     []string
	Changed     []string
	EnabledBots int
	Unchanged   bool
}

func (s *Server) previewSkillPublish(name string, files map[string][]byte) (skillPublishPreview, error) {
	var p skillPublishPreview
	row, found, err := s.store.libraryRow(name)
	if err != nil {
		return p, err
	}
	if !found {
		return p, errSkillNotFound
	}
	current, digest, err := s.extensions.SkillFilesMap(name)
	if err != nil {
		return p, err
	}
	var latest int
	if err := s.store.db.QueryRow(`SELECT COALESCE(MAX(version),?) FROM skill_versions WHERE name=?`, row.Version, name).Scan(&latest); err != nil {
		return p, err
	}
	p.Version = latest + 1
	p.Added, p.Removed, p.Changed = extensions.DiffSkillFiles(current, files)
	p.Unchanged = extensions.SkillFilesDigest(files) == digest
	err = s.store.db.QueryRow(`SELECT COUNT(*) FROM skill_enablement e JOIN bots b ON b.id=e.bot_id WHERE e.skill_name=? AND e.enabled=1 AND b.archived=0`, name).Scan(&p.EnabledBots)
	return p, err
}

// publishSkillVersion replaces a skill's files with a new version. Every Bot
// that has the skill on follows the newest version. A Bot may publish only
// skills it installed.
func (s *Server) publishSkillVersion(actor skillActor, name string, files map[string][]byte, note string) (int, error) {
	s.skillLibMu.Lock()
	defer s.skillLibMu.Unlock()
	row, found, err := s.store.libraryRow(name)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, errSkillNotFound
	}
	if actor.Kind == skillActorBot && (row.InstallerKind != skillActorBot || row.InstallerBotID != actor.BotID) {
		return 0, errSkillNotInstaller
	}
	preview, err := s.previewSkillPublish(name, files)
	if err != nil {
		return 0, err
	}
	if preview.Unchanged {
		return 0, errSkillUnchanged
	}
	current, curDigest, err := s.extensions.SkillFilesMap(name)
	if err != nil {
		return 0, err
	}
	// Both the version being left and the new one are archived first, so any
	// version can be restored later.
	if err := s.extensions.SaveSkillVersion(name, row.Version, current); err != nil {
		return 0, err
	}
	if err := s.extensions.SaveSkillVersion(name, preview.Version, files); err != nil {
		return 0, err
	}
	if err := s.extensions.ReplaceSkill(name, files); err != nil {
		return 0, err
	}
	digest := extensions.SkillFilesDigest(files)
	note = strings.TrimSpace(note)
	if r := []rune(note); len(r) > 300 {
		note = string(r[:300])
	}
	tx, err := s.store.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	// The version being left may be missing its row when the library row came
	// from an older reconcile; keep the history complete.
	if _, err = tx.Exec(`INSERT OR IGNORE INTO skill_versions(name,version,digest,created_by_kind,created_by_bot_id,created_at,note) VALUES(?,?,?,?,?,?,?)`,
		name, row.Version, curDigest, row.InstallerKind, row.InstallerBotID, row.InstalledAt, ""); err != nil {
		return 0, err
	}
	if err = insertVersionTx(tx, name, preview.Version, digest, actor, note); err != nil {
		return 0, err
	}
	if _, err = tx.Exec(`UPDATE skill_library SET current_version=?,digest=?,updated_at=? WHERE name=?`, preview.Version, digest, now(), name); err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	s.pruneSkillVersions(name)
	return preview.Version, nil
}

// pruneSkillVersions keeps the newest maxSkillVersions versions and the
// current one.
func (s *Server) pruneSkillVersions(name string) {
	rows, err := s.store.db.Query(`SELECT version FROM skill_versions WHERE name=? ORDER BY version DESC`, name)
	if err != nil {
		return
	}
	var all []int
	for rows.Next() {
		var v int
		if rows.Scan(&v) == nil {
			all = append(all, v)
		}
	}
	rows.Close()
	if len(all) <= maxSkillVersions {
		return
	}
	row, _, _ := s.store.libraryRow(name)
	keep := map[int]bool{row.Version: true}
	for _, v := range all[:maxSkillVersions] {
		keep[v] = true
	}
	for _, v := range all {
		if !keep[v] {
			_, _ = s.store.db.Exec(`DELETE FROM skill_versions WHERE name=? AND version=?`, name, v)
		}
	}
	_ = s.extensions.PruneSkillVersions(name, keep)
}

// rollbackSkill makes an earlier (or later) archived version the current one.
func (s *Server) rollbackSkill(name string, version int) error {
	s.skillLibMu.Lock()
	defer s.skillLibMu.Unlock()
	row, found, err := s.store.libraryRow(name)
	if err != nil {
		return err
	}
	if !found {
		return errSkillNotFound
	}
	var digest string
	if err := s.store.db.QueryRow(`SELECT digest FROM skill_versions WHERE name=? AND version=?`, name, version).Scan(&digest); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errSkillNotFound
		}
		return err
	}
	if row.Version == version {
		return errSkillUnchanged
	}
	files, err := s.extensions.ReadSkillVersion(name, version)
	if err != nil {
		return err
	}
	if err := s.extensions.ReplaceSkill(name, files); err != nil {
		return err
	}
	tx, err := s.store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE skill_library SET current_version=?,digest=?,updated_at=? WHERE name=?`, version, extensions.SkillFilesDigest(files), now(), name); err != nil {
		return err
	}
	return tx.Commit()
}

// deleteSkill removes a skill: its files and archive first, its rows second,
// so a failure in between leaves a row that the next reconcile drops, never a
// skill that is gone from the rows but still on disk.
func (s *Server) deleteSkill(name string) error {
	s.skillLibMu.Lock()
	defer s.skillLibMu.Unlock()
	return s.deleteSkillLocked(name)
}

func (s *Server) deleteSkillLocked(name string) error {
	if err := s.extensions.DeleteSkill(name); err != nil {
		return err
	}
	_ = s.extensions.RemoveSkillVersions(name)
	tx, err := s.store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{`DELETE FROM skill_library WHERE name=?`, `DELETE FROM skill_versions WHERE name=?`, `DELETE FROM skill_enablement WHERE skill_name=?`} {
		if _, err = tx.Exec(q, name); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`DELETE FROM skill_bundles WHERE id NOT IN (SELECT bundle_id FROM skill_library WHERE bundle_id<>'')`); err != nil {
		return err
	}
	return tx.Commit()
}

// deleteSkillBundle removes a bundle and every skill in it.
func (s *Server) deleteSkillBundle(id string) error {
	s.skillLibMu.Lock()
	defer s.skillLibMu.Unlock()
	members, err := s.store.bundleMembers(id)
	if err != nil {
		return err
	}
	if _, found, err := s.store.bundleRow(id); err != nil || !found {
		if err != nil {
			return err
		}
		return errSkillNotFound
	}
	for _, name := range members {
		if err := s.deleteSkillLocked(name); err != nil {
			return err
		}
	}
	_, err = s.store.db.Exec(`DELETE FROM skill_bundles WHERE id=?`, id)
	return err
}

// ---------------------------------------------------------------------------
// Reconcile and one-time migration

// reconcileSkillLibrary makes the table agree with the files: a skill on disk
// without a row gets one (installer unknown), a row whose folder is gone is
// dropped, and a changed digest is recorded. Files win. The returned lines
// are diagnostics, also written to the log.
func (s *Server) reconcileSkillLibrary() ([]string, error) {
	s.skillLibMu.Lock()
	defer s.skillLibMu.Unlock()
	return s.reconcileSkillLibraryLocked()
}

func (s *Server) reconcileSkillLibraryLocked() ([]string, error) {
	if s.extensions == nil {
		return nil, nil
	}
	views, _ := s.extensions.ListSkills()
	onDisk := map[string]bool{}
	for _, v := range views {
		if !v.Builtin {
			onDisk[v.Name] = true
		}
	}
	rows, err := s.store.libraryRows()
	if err != nil {
		return nil, err
	}
	known := map[string]libraryRow{}
	for _, r := range rows {
		known[r.Name] = r
	}
	var diags []string
	type change struct {
		kind, name, digest string
	}
	var changes []change
	for name := range onDisk {
		_, digest, derr := s.extensions.SkillFilesMap(name)
		if derr != nil {
			digest = ""
		}
		r, ok := known[name]
		switch {
		case !ok:
			changes = append(changes, change{"add", name, digest})
			diags = append(diags, fmt.Sprintf("skill %q is on disk but was not in the library; added with an unknown installer and turned on for no Bot", name))
		case digest != "" && r.Digest != "" && r.Digest != digest:
			changes = append(changes, change{"digest", name, digest})
			diags = append(diags, fmt.Sprintf("skill %q files changed outside TOFI; the library now records the files on disk", name))
		case digest != "" && r.Digest == "":
			changes = append(changes, change{"digest", name, digest})
		}
	}
	for _, r := range rows {
		if !onDisk[r.Name] && !s.extensions.SkillExists(r.Name) {
			changes = append(changes, change{"drop", r.Name, ""})
			diags = append(diags, fmt.Sprintf("skill %q is in the library but its files are gone; the row was dropped", r.Name))
		}
	}
	if len(changes) == 0 {
		return nil, nil
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].name < changes[j].name })
	tx, err := s.store.db.Begin()
	if err != nil {
		return diags, err
	}
	defer tx.Rollback()
	for _, c := range changes {
		switch c.kind {
		case "add":
			if err = insertLibraryTx(tx, c.name, skillActor{Kind: skillActorLegacy}, "", "disk", c.digest); err != nil {
				return diags, err
			}
		case "digest":
			if _, err = tx.Exec(`UPDATE skill_library SET digest=?,updated_at=? WHERE name=?`, c.digest, now(), c.name); err != nil {
				return diags, err
			}
			if _, err = tx.Exec(`UPDATE skill_versions SET digest=? WHERE name=? AND version=(SELECT current_version FROM skill_library WHERE name=?)`, c.digest, c.name, c.name); err != nil {
				return diags, err
			}
		case "drop":
			for _, q := range []string{`DELETE FROM skill_library WHERE name=?`, `DELETE FROM skill_versions WHERE name=?`, `DELETE FROM skill_enablement WHERE skill_name=?`} {
				if _, err = tx.Exec(q, c.name); err != nil {
					return diags, err
				}
			}
		}
	}
	if _, err = tx.Exec(`DELETE FROM skill_bundles WHERE id NOT IN (SELECT bundle_id FROM skill_library WHERE bundle_id<>'')`); err != nil {
		return diags, err
	}
	if err = tx.Commit(); err != nil {
		return diags, err
	}
	for _, d := range diags {
		log.Printf("[skills] %s", d)
	}
	return diags, nil
}

// migrateSkillLibraryData runs once per workspace (marker). It turns the old
// model into the new one without changing what any existing Bot can use:
//
//   - a skill that was open to every Bot (no skill_access rows) is turned on
//     for every existing Bot;
//   - a skill limited to listed Bots is turned on for those Bots only;
//   - an official skill the owner had switched off globally is turned off for
//     every existing Bot (the global switch is gone).
//
// Archived Bots are included so a restored Bot keeps what it had. Bots made
// later start with the official skills only. skill_access and builtin_skill_off
// are read here and nowhere else; they are kept, not dropped.
func (s *Server) migrateSkillLibraryData() error {
	s.skillLibMu.Lock()
	defer s.skillLibMu.Unlock()
	var done int
	if err := s.store.db.QueryRow(`SELECT COUNT(*) FROM skill_library_markers WHERE marker=?`, skillMigrationMarker).Scan(&done); err != nil {
		return err
	}
	if done > 0 {
		return nil
	}
	if _, err := s.reconcileSkillLibraryLocked(); err != nil {
		return err
	}
	rows, err := s.store.libraryRows()
	if err != nil {
		return err
	}
	access, err := s.store.legacySkillAccess()
	if err != nil {
		return err
	}
	off, err := s.store.legacyBuiltinOff()
	if err != nil {
		return err
	}
	bots, err := s.store.botNames()
	if err != nil {
		return err
	}
	botIDs := make([]string, 0, len(bots))
	for id := range bots {
		botIDs = append(botIDs, id)
	}
	sort.Strings(botIDs)
	tx, err := s.store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range rows {
		listed := access[r.Name]
		for _, id := range botIDs {
			if len(listed) == 0 || listed[id] {
				if err = setSkillEnabledTx(tx, id, r.Name, true, skillSetByMigration); err != nil {
					return err
				}
			}
		}
	}
	for name := range off {
		if !extensions.IsBuiltinSkill(name) {
			continue
		}
		for _, id := range botIDs {
			if err = setSkillEnabledTx(tx, id, name, false, skillSetByMigration); err != nil {
				return err
			}
		}
	}
	if _, err = tx.Exec(`INSERT INTO skill_library_markers(marker,done_at) VALUES(?,?)`, skillMigrationMarker, now()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) legacySkillAccess() (map[string]map[string]bool, error) {
	out := map[string]map[string]bool{}
	rows, err := s.db.Query(`SELECT skill_name, bot_id FROM skill_access`)
	if err != nil {
		// A workspace that never had the table has nothing to carry over.
		if strings.Contains(err.Error(), "no such table") {
			return out, nil
		}
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var skill, bot string
		if err := rows.Scan(&skill, &bot); err != nil {
			return nil, err
		}
		if out[skill] == nil {
			out[skill] = map[string]bool{}
		}
		out[skill][bot] = true
	}
	return out, rows.Err()
}

func (s *Store) legacyBuiltinOff() (map[string]bool, error) {
	out := map[string]bool{}
	rows, err := s.db.Query(`SELECT skill_name FROM builtin_skill_off`)
	if err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return out, nil
		}
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Views

func (s *Server) installerView(kind, botID string, names map[string]string) *skillInstaller {
	return &skillInstaller{Kind: kind, BotID: botID, BotName: names[botID]}
}

// libraryListing returns the whole library for Settings: official skills first
// in name order with installed ones, bundles, and how many active Bots have
// each skill on.
func (s *Server) libraryListing() ([]librarySkillView, []libraryBundleView, []extensions.Diagnostic, error) {
	views, diags := s.extensions.ListSkills()
	rows, err := s.store.libraryRows()
	if err != nil {
		return nil, nil, nil, err
	}
	known := map[string]libraryRow{}
	for _, r := range rows {
		known[r.Name] = r
	}
	names, err := s.store.botNames()
	if err != nil {
		return nil, nil, nil, err
	}
	active, err := s.store.activeBotSet()
	if err != nil {
		return nil, nil, nil, err
	}
	enabled := map[string]int{}
	offBots := map[string]int{}
	erows, err := s.store.db.Query(`SELECT bot_id,skill_name,enabled FROM skill_enablement`)
	if err != nil {
		return nil, nil, nil, err
	}
	for erows.Next() {
		var bot, skill string
		var on int
		if err := erows.Scan(&bot, &skill, &on); err != nil {
			erows.Close()
			return nil, nil, nil, err
		}
		if !active[bot] {
			continue
		}
		if on != 0 {
			enabled[skill]++
		} else {
			offBots[skill]++
		}
	}
	erows.Close()
	versions := map[string][]skillVersionView{}
	vrows, err := s.store.db.Query(`SELECT name,version,created_at,created_by_kind,created_by_bot_id,note FROM skill_versions ORDER BY name,version DESC`)
	if err != nil {
		return nil, nil, nil, err
	}
	for vrows.Next() {
		var name string
		var v skillVersionView
		var kind, bot string
		if err := vrows.Scan(&name, &v.Version, &v.CreatedAt, &kind, &bot, &v.Note); err != nil {
			vrows.Close()
			return nil, nil, nil, err
		}
		v.CreatedBy = *s.installerView(kind, bot, names)
		versions[name] = append(versions[name], v)
	}
	vrows.Close()
	out := make([]librarySkillView, 0, len(views))
	for _, v := range views {
		item := librarySkillView{SkillView: v, BotsTotal: len(active)}
		if v.Builtin {
			item.EnabledBots = len(active) - offBots[v.Name]
		} else {
			item.EnabledBots = enabled[v.Name]
			if r, ok := known[v.Name]; ok {
				item.BundleID, item.InstalledAt, item.Version = r.BundleID, r.InstalledAt, r.Version
				item.InstalledBy = s.installerView(r.InstallerKind, r.InstallerBotID, names)
				item.Updated = r.Version > 1
				if item.Updated {
					item.UpdatedAt = r.UpdatedAt
				}
				for _, ver := range versions[v.Name] {
					ver.Current = ver.Version == r.Version
					item.Versions = append(item.Versions, ver)
				}
				if len(item.Versions) < 2 {
					item.Versions = nil
				}
			}
		}
		out = append(out, item)
	}
	brows, err := s.store.db.Query(`SELECT id FROM skill_bundles ORDER BY id`)
	if err != nil {
		return nil, nil, nil, err
	}
	var ids []string
	for brows.Next() {
		var id string
		if brows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	brows.Close()
	bundles := make([]libraryBundleView, 0, len(ids))
	for _, id := range ids {
		b, ok, err := s.store.bundleRow(id)
		if err != nil || !ok {
			continue
		}
		b.InstalledBy = s.installerView(b.InstalledBy.Kind, b.InstalledBy.BotID, names)
		if b.Skills, err = s.store.bundleMembers(id); err != nil {
			return nil, nil, nil, err
		}
		bundles = append(bundles, b)
	}
	return out, bundles, diags, nil
}

type botSkillView struct {
	extensions.SkillView
	BundleID string `json:"bundle_id,omitempty"`
	Enabled  bool   `json:"enabled"`
	// SetBy is owner, bot (the Bot itself) or migration; empty for an
	// official skill nobody has changed.
	SetBy string `json:"set_by,omitempty"`
	SetAt string `json:"set_at,omitempty"`
}

// botSkillStates is the Bot settings view: the whole library with this Bot's
// choice on each skill.
func (s *Server) botSkillStates(botID string) ([]botSkillView, []libraryBundleView, error) {
	views, _ := s.extensions.ListSkills()
	rows, err := s.store.libraryRows()
	if err != nil {
		return nil, nil, err
	}
	bundleOf := map[string]string{}
	for _, r := range rows {
		bundleOf[r.Name] = r.BundleID
	}
	type state struct {
		on     bool
		by, at string
	}
	states := map[string]state{}
	srows, err := s.store.db.Query(`SELECT skill_name,enabled,set_by,set_at FROM skill_enablement WHERE bot_id=?`, botID)
	if err != nil {
		return nil, nil, err
	}
	for srows.Next() {
		var name string
		var on int
		var st state
		if err := srows.Scan(&name, &on, &st.by, &st.at); err != nil {
			srows.Close()
			return nil, nil, err
		}
		st.on = on != 0
		states[name] = st
	}
	srows.Close()
	out := make([]botSkillView, 0, len(views))
	for _, v := range views {
		item := botSkillView{SkillView: v, BundleID: bundleOf[v.Name]}
		st, set := states[v.Name]
		if v.Builtin {
			item.Enabled = !set || st.on
		} else {
			item.Enabled = set && st.on
		}
		if set {
			item.SetBy, item.SetAt = st.by, st.at
		}
		out = append(out, item)
	}
	_, bundles, _, err := s.libraryListing()
	return out, bundles, err
}

// skillLibraryError maps a library error to an HTTP status and code.
func skillLibraryError(err error) (int, string, string) {
	switch {
	case errors.Is(err, extensions.ErrBuiltinSkill):
		return 400, "builtin_skill", err.Error()
	case errors.Is(err, extensions.ErrSkillExists):
		return 409, "skill_exists", err.Error()
	case errors.Is(err, errSkillNotFound):
		return 404, "not_found", err.Error()
	case errors.Is(err, errSkillInBundle):
		return 409, "skill_in_bundle", err.Error()
	case errors.Is(err, errSkillUnchanged):
		return 409, "skill_unchanged", err.Error()
	case errors.Is(err, errSkillNotInstaller):
		return 403, "skill_not_installer", err.Error()
	}
	if o, ok := tooloutcome.FromError(err); ok {
		return 400, "extensions", o.Message
	}
	return 400, "extensions", err.Error()
}

// activeBotSet returns the ids of existing, non-archived Bots.
func (s *Store) activeBotSet() (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT id FROM bots WHERE archived=0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
