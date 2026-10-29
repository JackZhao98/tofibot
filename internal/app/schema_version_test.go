package app

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// dbDirs are the two kinds of database the server opens.
func schemaDirs(t *testing.T) map[string]string {
	root := t.TempDir()
	return map[string]string{
		"instance": root,
		"account":  filepath.Join(root, "accounts", "3f2a9c1e-0000-4000-8000-000000000001"),
	}
}

func rawDB(t *testing.T, dir string) *sql.DB {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "tofi.db")+sqliteBusyDSN)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	return db
}

func schemaOf(t *testing.T, dir string) (user int, table, appBy string) {
	t.Helper()
	db := rawDB(t, dir)
	defer db.Close()
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	_ = db.QueryRow(`SELECT value FROM tofi_schema WHERE key='schema_version'`).Scan(&table)
	_ = db.QueryRow(`SELECT value FROM tofi_schema WHERE key='app_version'`).Scan(&appBy)
	return
}

func fileBytes(t *testing.T, dir string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "tofi.db"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestFreshDatabaseIsStampedWithSchemaAndAppVersion(t *testing.T) {
	old := Version
	Version = "v9.9.9"
	defer func() { Version = old }()
	for name, dir := range schemaDirs(t) {
		t.Run(name, func(t *testing.T) {
			s, err := OpenStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			s.db.Close()
			user, table, appBy := schemaOf(t, dir)
			if user != SchemaVersion || table != strconv.Itoa(SchemaVersion) || appBy != "v9.9.9" {
				t.Fatalf("user_version=%d schema_version=%q app_version=%q", user, table, appBy)
			}
			// A second open of a current database changes nothing.
			s, err = OpenStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			s.db.Close()
		})
	}
}

// A database that predates the stamp (every database in the field before this
// guard) is the baseline: it is migrated forward and stamped, data intact.
func TestUnstampedLegacyDatabaseMigratesForwardAndKeepsData(t *testing.T) {
	for name, dir := range schemaDirs(t) {
		t.Run(name, func(t *testing.T) {
			db := rawDB(t, dir)
			// The oldest shape migrate() can still open: the tables it creates
			// with IF NOT EXISTS, without the columns it adds with ensureColumn
			// (messages.kind stays: an index is created over it before the add).
			if _, err := db.Exec(`PRAGMA journal_mode=WAL;
CREATE TABLE bots(id TEXT PRIMARY KEY,name TEXT NOT NULL,instructions TEXT NOT NULL,model TEXT NOT NULL,dm_conversation_id TEXT NOT NULL UNIQUE,created_at TEXT NOT NULL);
CREATE TABLE conversations(id TEXT PRIMARY KEY,kind TEXT NOT NULL,name TEXT NOT NULL,bot_id TEXT,updated_at TEXT NOT NULL,archived INTEGER NOT NULL DEFAULT 0);
CREATE TABLE messages(id TEXT PRIMARY KEY,conversation_id TEXT NOT NULL,seq INTEGER NOT NULL,role TEXT NOT NULL,kind TEXT NOT NULL DEFAULT '',sender_bot_id TEXT,run_id TEXT,content TEXT NOT NULL,created_at TEXT NOT NULL,client_message_id TEXT,UNIQUE(conversation_id,seq));
CREATE TABLE runs(id TEXT PRIMARY KEY,conversation_id TEXT NOT NULL,bot_id TEXT NOT NULL,status TEXT NOT NULL,error TEXT,parent_run_id TEXT,created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
CREATE TABLE memories(id TEXT PRIMARY KEY,conversation_id TEXT NOT NULL,bot_id TEXT,content TEXT NOT NULL,revision INTEGER NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL);
INSERT INTO bots(id,name,instructions,model,dm_conversation_id,created_at) VALUES('b1','Old','be kind','m','c1','2025-01-01');
INSERT INTO conversations(id,kind,name,bot_id,updated_at) VALUES('c1','dm','dm','b1','2025-01-01');
INSERT INTO messages(id,conversation_id,seq,role,content,created_at) VALUES('m1','c1',1,'user','hello from the past','2025-01-01');
INSERT INTO runs(id,conversation_id,bot_id,status,created_at,updated_at) VALUES('r1','c1','b1','done','2025-01-01','2025-01-01');
INSERT INTO memories(id,conversation_id,bot_id,content,revision,created_at,updated_at) VALUES('mem1','c1','b1','remember',1,'2025-01-01','2025-01-01');`); err != nil {
				t.Fatal(err)
			}
			db.Close()
			s, err := OpenStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			var content string
			if err := s.db.QueryRow(`SELECT content FROM messages WHERE id='m1'`).Scan(&content); err != nil || content != "hello from the past" {
				t.Fatalf("message lost: %q %v", content, err)
			}
			var kind string
			if err := s.db.QueryRow(`SELECT final_origin FROM runs WHERE id='r1'`).Scan(&kind); err != nil {
				t.Fatalf("column runs.final_origin was not added: %v", err)
			}
			s.db.Close()
			if user, table, _ := schemaOf(t, dir); user != SchemaVersion || table != strconv.Itoa(SchemaVersion) {
				t.Fatalf("not stamped: %d %q", user, table)
			}
		})
	}
}

func TestMigrationsAreIdempotent(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := s.migrate(); err != nil {
			t.Fatalf("migrate run %d: %v", i+2, err)
		}
	}
	s.db.Close()
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.db.Close()
}

func TestNewerDatabaseIsRefusedAndNeverWritten(t *testing.T) {
	for name, dir := range schemaDirs(t) {
		for _, form := range []string{"user_version", "table"} {
			t.Run(name+"/"+form, func(t *testing.T) {
				if name == "account" {
					dir = filepath.Join(filepath.Dir(dir), "x-"+form)
				} else {
					dir = filepath.Join(dir, form)
				}
				db := rawDB(t, dir)
				next := strconv.Itoa(SchemaVersion + 1)
				stmt := `PRAGMA user_version=` + next
				if form == "table" {
					stmt = `CREATE TABLE tofi_schema(key TEXT PRIMARY KEY,value TEXT NOT NULL); INSERT INTO tofi_schema VALUES('schema_version','` + next + `'),('app_version','v9.0.0')`
				}
				if _, err := db.Exec(`CREATE TABLE keep(x); INSERT INTO keep VALUES(1);` + stmt); err != nil {
					t.Fatal(err)
				}
				db.Close()
				before := fileBytes(t, dir)
				_, err := OpenStore(dir)
				var se *SchemaError
				if !errors.As(err, &se) || se.Kind != schemaTooNew || se.Found != SchemaVersion+1 || se.Supported != SchemaVersion {
					t.Fatalf("want too_new SchemaError, got %v", err)
				}
				msg := err.Error()
				for _, want := range []string{filepath.Join(dir, "tofi.db"), "schema version " + next, "supports up to " + strconv.Itoa(SchemaVersion), "sudo tofi update", "sudo tofi restore"} {
					if !strings.Contains(msg, want) {
						t.Errorf("message lacks %q: %s", want, msg)
					}
				}
				if name == "account" && !strings.Contains(msg, "account database") || name == "instance" && !strings.Contains(msg, "instance database") {
					t.Errorf("message does not name the database kind: %s", msg)
				}
				if string(fileBytes(t, dir)) != string(before) {
					t.Fatal("a refused database was modified")
				}
				if _, statErr := os.Stat(filepath.Join(dir, "tofi.db-wal")); statErr == nil {
					t.Fatal("a refused database got a WAL file")
				}
			})
		}
	}
}

func TestGarbledVersionIsRefused(t *testing.T) {
	cases := map[string]string{
		"text":       `CREATE TABLE tofi_schema(key TEXT PRIMARY KEY,value TEXT NOT NULL); INSERT INTO tofi_schema VALUES('schema_version','banana')`,
		"negative":   `CREATE TABLE tofi_schema(key TEXT PRIMARY KEY,value TEXT NOT NULL); INSERT INTO tofi_schema VALUES('schema_version','-3')`,
		"empty":      `CREATE TABLE tofi_schema(key TEXT PRIMARY KEY,value TEXT NOT NULL); INSERT INTO tofi_schema VALUES('schema_version','')`,
		"float":      `CREATE TABLE tofi_schema(key TEXT PRIMARY KEY,value TEXT NOT NULL); INSERT INTO tofi_schema VALUES('schema_version','1.5')`,
		"no row":     `CREATE TABLE tofi_schema(key TEXT PRIMARY KEY,value TEXT NOT NULL); INSERT INTO tofi_schema VALUES('app_version','v1')`,
		"disagree":   `CREATE TABLE tofi_schema(key TEXT PRIMARY KEY,value TEXT NOT NULL); INSERT INTO tofi_schema VALUES('schema_version','1'); PRAGMA user_version=7`,
		"negative_u": `PRAGMA user_version=-5`,
	}
	for name, dir := range schemaDirs(t) {
		for label, stmt := range cases {
			t.Run(name+"/"+label, func(t *testing.T) {
				dir := filepath.Join(dir, strings.ReplaceAll(label, " ", "_"))
				if name == "account" {
					dir = filepath.Join(filepath.Dir(dir), "accounts", "x-"+strings.ReplaceAll(label, " ", "_"))
				}
				db := rawDB(t, dir)
				if _, err := db.Exec(`CREATE TABLE keep(x);` + stmt); err != nil {
					t.Fatal(err)
				}
				db.Close()
				before := fileBytes(t, dir)
				_, err := OpenStore(dir)
				var se *SchemaError
				if !errors.As(err, &se) || se.Kind != schemaGarble {
					t.Fatalf("want garbled SchemaError, got %v", err)
				}
				if !strings.Contains(err.Error(), "schema_version_unreadable") || !strings.Contains(err.Error(), filepath.Join(dir, "tofi.db")) || !strings.Contains(err.Error(), "sudo tofi restore") {
					t.Errorf("message not named: %s", err)
				}
				if string(fileBytes(t, dir)) != string(before) {
					t.Fatal("a refused database was modified")
				}
			})
		}
	}
}

func TestServerStartRefusesNewerAccountDatabase(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "accounts", "3f2a9c1e-0000-4000-8000-000000000001")
	db := rawDB(t, dir)
	if _, err := db.Exec(`PRAGMA user_version=` + strconv.Itoa(SchemaVersion+4)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	err := preflightAccountSchemas(root)
	var se *SchemaError
	if !errors.As(err, &se) || se.Kind != schemaTooNew || !strings.Contains(err.Error(), "account database") {
		t.Fatalf("preflight: %v", err)
	}
	if err := preflightAccountSchemas(t.TempDir()); err != nil {
		t.Fatalf("no accounts directory must pass: %v", err)
	}
}

func TestPurgeKeepsTheSchemaRecord(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.purgeWorkspaceData(); err != nil {
		t.Fatal(err)
	}
	s.db.Close()
	if user, table, _ := schemaOf(t, dir); user != SchemaVersion || table != strconv.Itoa(SchemaVersion) {
		t.Fatalf("purge lost the version record: %d %q", user, table)
	}
	s, err = OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.db.Close()
}

func TestDockerfileLabelMatchesSchemaVersion(t *testing.T) {
	b, err := os.ReadFile("../../Dockerfile")
	if err != nil {
		t.Skip("Dockerfile not available")
	}
	if !strings.Contains(string(b), `io.tofi.data-schema="`+DataSchemaLabel()+`"`) {
		t.Fatalf("Dockerfile label io.tofi.data-schema must be %q (SchemaVersion=%d); change both together", DataSchemaLabel(), SchemaVersion)
	}
}
