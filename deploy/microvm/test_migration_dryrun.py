import builtins
import json
import sqlite3
import tempfile
import unittest
from pathlib import Path
from unittest import mock

import migration_dryrun
from migration_dryrun import DryRunError, build_report


class MigrationDryRunAcceptance(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.data = self.root / "legacy-data"
        (self.data / "attachments").mkdir(parents=True)
        self.db = sqlite3.connect(self.data / "tofi.db")
        self.db.executescript("""
            CREATE TABLE workspace_owner(id INTEGER PRIMARY KEY CHECK(id=1), username TEXT NOT NULL,
                email TEXT NOT NULL, salt BLOB NOT NULL, password_hash BLOB NOT NULL, created_at INTEGER NOT NULL);
            INSERT INTO workspace_owner VALUES(1,'legacy','legacy@example.test',x'01',x'02',1);
            CREATE TABLE accounts(id TEXT PRIMARY KEY,username TEXT NOT NULL COLLATE NOCASE UNIQUE,
                email TEXT NOT NULL COLLATE NOCASE UNIQUE,role TEXT NOT NULL CHECK(role IN ('admin','user')),
                salt BLOB NOT NULL,password_hash BLOB NOT NULL,disabled INTEGER NOT NULL DEFAULT 0,
                must_change_password INTEGER NOT NULL DEFAULT 0,legacy INTEGER NOT NULL DEFAULT 0,created_at INTEGER NOT NULL);
            INSERT INTO accounts(id,username,email,role,salt,password_hash,legacy,created_at)
                VALUES('legacy-owner','legacy','legacy@example.test','admin',x'01',x'02',1,1);
            CREATE TABLE attachments(
                id TEXT PRIMARY KEY, conversation_id TEXT NOT NULL, name TEXT NOT NULL,
                mime TEXT NOT NULL, size INTEGER NOT NULL, disk_name TEXT NOT NULL UNIQUE,
                created_at TEXT NOT NULL);
            CREATE TABLE attachment_messages(
                attachment_id TEXT NOT NULL, message_id TEXT NOT NULL,
                PRIMARY KEY(attachment_id,message_id));
            INSERT INTO attachments VALUES('asset-1','conversation-1','legacy.txt','text/plain',16,'asset-1.upload','2026-01-01T00:00:00Z');
        """)
        self.db.commit()
        self.db.close()
        (self.data / "attachments" / "asset-1.upload").write_bytes(b"preserved bytes!")
        self.computer = self.root / "personal.json"
        self.state = self.root / "computer-state"
        self.state.mkdir()
        (self.state / "workspace.ext4").write_bytes(b"fixture disk bytes")
        self.computer.write_text(json.dumps({"id": "personal", "state_dir": str(self.state)}))
        self.runner_state = self.root / "runner-state"
        self.runner_auth = self.root / "runner-auth"
        self.runner_state.mkdir()
        self.runner_auth.mkdir()
        (self.runner_state / "plugin-state.json").write_text('{"fixture":true}')
        (self.runner_auth / "token").write_text("synthetic-token")
        self.destination = self.root / "future-destination"

    def tearDown(self):
        self.temp.cleanup()

    def report(self):
        return build_report(self.data, self.computer, self.runner_state, self.runner_auth, self.destination)

    def test_populated_fixture_yields_stable_mapping_manifest_and_unresolved_handoff(self):
        before = {p: p.read_bytes() for p in [self.data / "tofi.db", self.data / "attachments" / "asset-1.upload",
                                               self.state / "workspace.ext4", self.runner_state / "plugin-state.json",
                                               self.runner_auth / "token"]}
        report = self.report()
        self.assertEqual(report["integrity"]["sqlite"], "ok")
        self.assertEqual(report["integrity"]["attachments"], "ok")
        self.assertEqual(report["legacy_owner"]["account_id"], "legacy-owner")
        self.assertEqual(report["proposed_computer_uuid"], self.report()["proposed_computer_uuid"])
        self.assertIn("proposed only", report["proposed_computer_uuid_basis"])
        self.assertEqual(report["handoff_ownership"]["status"], "unresolved")
        self.assertEqual([item["domain"] for item in report["handoff_ownership"]["items"]],
                         ["personal_computer_handoff", "runner_state_handoff", "runner_auth_handoff"])
        self.assertFalse(report["ready_for_copy"])
        self.assertEqual(report["activation"]["state"], "not-planned")
        self.assertFalse(self.destination.exists())
        self.assertEqual(before, {p: p.read_bytes() for p in before})
        self.assertTrue(any(item["path"] == "attachments/asset-1.upload"
                            for item in report["preservation_manifest"]["legacy_app_data"]["files"]))
        self.assertNotIn("synthetic-token", json.dumps(report))

    def test_attachment_size_mismatch_fails_integrity_without_writing_destination(self):
        (self.data / "attachments" / "asset-1.upload").write_bytes(b"short")
        report = self.report()
        self.assertEqual(report["integrity"]["attachments"], "mismatch")
        self.assertTrue(any("size mismatch" in item for item in report["integrity"]["problems"]))
        self.assertFalse(report["ready_for_copy"])
        self.assertFalse(self.destination.exists())

    def test_destination_collision_is_a_hard_error(self):
        expected = self.report()["destination_plan"][0]["destination"]
        target = Path(expected)
        target.parent.mkdir(parents=True)
        target.mkdir()
        with self.assertRaisesRegex(DryRunError, "destination collision"):
            self.report()

    def test_attachment_path_traversal_is_reported(self):
        db = sqlite3.connect(self.data / "tofi.db")
        db.execute("UPDATE attachments SET disk_name='../outside' WHERE id='asset-1'")
        db.commit()
        db.close()
        report = self.report()
        self.assertTrue(any("unsafe attachment disk_name" in item for item in report["integrity"]["problems"]))
        self.assertFalse(report["ready_for_copy"])

    def test_source_changes_during_inventory_fail_closed(self):
        original = migration_dryrun._hash_file
        changed = False

        def modify_source(path):
            nonlocal changed
            if Path(path).resolve() == (self.data / "attachments" / "asset-1.upload").resolve() and not changed:
                changed = True
                Path(path).write_bytes(b"changed while reading")
            return original(path)

        with mock.patch("migration_dryrun._hash_file", side_effect=modify_source):
            with self.assertRaisesRegex(DryRunError, "source tree changed during dry-run"):
                self.report()

    def test_file_hash_checks_stat_before_and_after_read(self):
        source = self.root / "changing-file"
        source.write_bytes(b"before")
        open_file = Path.open
        mutated = False

        class Reader:
            def __init__(self, stream):
                self.stream = stream

            def __enter__(self):
                return self

            def __exit__(self, *args):
                self.stream.close()

            def read(self, amount):
                nonlocal mutated
                if not mutated:
                    mutated = True
                    with builtins.open(source, "wb") as replacement:
                        replacement.write(b"after-change")
                return self.stream.read(amount)

        def open_with_concurrent_change(path, *args, **kwargs):
            stream = open_file(path, *args, **kwargs)
            if path == source:
                return Reader(stream)
            return stream

        with mock.patch.object(Path, "open", open_with_concurrent_change):
            with self.assertRaisesRegex(DryRunError, "source file changed while hashing"):
                migration_dryrun._hash_file(source)

    def test_nonempty_wal_is_clearly_blocked_without_creating_backup(self):
        wal = Path(str(self.data / "tofi.db") + "-wal")
        wal.write_bytes(b"fixture wal data")
        before = set(self.data.iterdir())
        with self.assertRaisesRegex(DryRunError, r"blocked: legacy database has a non-empty WAL \(16 bytes\)"):
            self.report()
        self.assertEqual(before, set(self.data.iterdir()))
        self.assertFalse(self.destination.exists())

    def test_computer_metadata_nested_in_workspace_is_only_copied_with_parent(self):
        nested = self.state / "personal.json"
        nested.write_text(json.dumps({"id": "personal", "state_dir": str(self.state)}))
        self.computer = nested
        report = self.report()
        categories = [item["category"] for item in report["destination_plan"]]
        self.assertNotIn("computer_metadata", categories)
        workspace = next(item for item in report["destination_plan"] if item["category"] == "personal_workspace")
        self.assertEqual(workspace["included_assets"], [{"category": "computer_metadata", "source": str(nested.resolve())}])


if __name__ == "__main__":
    unittest.main()
