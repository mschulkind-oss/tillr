import json
from pathlib import Path
import sqlite3
from tempfile import TemporaryDirectory
import unittest

from handoff import report


class HandoffReportTest(unittest.TestCase):
    def test_measured_and_old_runs_join_by_sequence_and_lane(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            db = root / "state.db"
            with sqlite3.connect(db) as conn:
                conn.execute("CREATE TABLE events (seq INTEGER PRIMARY KEY, ts, kind, object_id, payload)")
                rows = [
                    (100, "run.finish", "r1", {"lane_id": "lane-1", "item_id": "a"}),
                    (101, "run.finish", "peer", {"lane_id": "lane-1", "item_id": "a"}),
                    (113, "drain.examination-claimed", "r1", {}),
                    (115, "run.allocate", "lane-2", {"run_id": "irrelevant"}),
                    (160, "drain.node-advanced", "r2", {"parent_run_id": "r1"}),
                    (170, "run.allocate", "lane-1", {"run_id": "r2", "item_id": "a"}),
                    (165, "run.finish", "r2", {"lane_id": "lane-1"}),
                    (180, "drain.examination-claimed", "r2", {}),
                    (200, "drain.examined", "r2", {}),
                    (220, "run.finish", "unexamined", {"lane_id": "lane-2"}),
                ]
                conn.executemany("INSERT INTO events(ts,kind,object_id,payload) VALUES(?,?,?,?)", (
                    (ts, kind, ident, json.dumps(data)) for ts, kind, ident, data in rows
                ))
            (root / "r1").mkdir()
            (root / "r1" / "handoff.jsonl").write_text("\n".join(map(json.dumps, [
                {"phase": "boundary", "start_ms": 103, "end_ms": 110},
                {"phase": "examination", "start_ms": 120, "end_ms": 164, "phases": [
                    {"phase": "store_write", "start_ms": 122, "end_ms": 127},
                    {"phase": "store_write", "start_ms": 125, "end_ms": 131},
                    {"phase": "candidate_gate", "start_ms": 130, "end_ms": 150},
                    {"phase": "pack_mirror", "start_ms": 150, "end_ms": 155},
                ]},
            ])))
            (root / "peer").mkdir()
            (root / "peer" / "handoff.jsonl").write_text(json.dumps({
                "phase": "examination", "start_ms": 120, "end_ms": 164, "primary_run_id": "r1",
            }))
            first, peer, old, unexamined = report(db, root)
            self.assertEqual(peer["finish_to_examination_start_ms"], 19)
            self.assertEqual(peer["examined_to_next_allocate_ms"], 10)
            self.assertEqual(first["finish_to_examination_start_ms"], 20)
            self.assertEqual(first["post_finish_boundary_ms"], 7)
            self.assertEqual(first["examination_ms"], 44)
            self.assertEqual(first["examination_phases_ms"], {
                "store_write": 9, "candidate_gate": 20, "pack_mirror": 5, "other": 11,
            })
            self.assertEqual(first["examined_to_next_allocate_ms"], 10)
            self.assertEqual(first["finish_to_next_allocate_ms"], 70)
            self.assertEqual(first["next_run_id"], "r2")
            self.assertEqual(old["examination_start_source"], "claim")
            self.assertIsNone(old["examination_phases_ms"])
            self.assertIsNone(old["examined_to_next_allocate_ms"])
            self.assertIsNone(unexamined["finish_to_examination_start_ms"])
            self.assertEqual(list(report(db, root, since=100, until=110)), [first, peer])


if __name__ == "__main__":
    unittest.main()
