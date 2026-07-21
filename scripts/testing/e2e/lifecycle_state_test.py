#!/usr/bin/env python3

import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


HERE = Path(__file__).resolve().parent
TOOL = HERE / "lifecycle_state.py"
SHA = "a" * 40
UUID = "b" * 32
TREE_ID = "d" * 64
STAGES = [
    "fixture",
    "host-preflight",
    "network-start",
    "el1",
    "el2",
    "deposit",
    "system-base",
    "system-signer",
    "system-participant",
    "fresh-snap",
    "fresh-full",
    "cleanup",
]


class LifecycleStateTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.state = Path(self.temporary.name) / "state.json"
        self.run_tool(
            "init",
            "--file",
            str(self.state),
            "--source-sha",
            SHA,
            "--enclave-name",
            "vm64-test",
            "--enclave-uuid",
            UUID,
            "--dump-dir",
            str(Path(self.temporary.name) / "dump"),
            "--tree-id",
            TREE_ID,
        )

    def run_tool(self, *args, check=True):
        return subprocess.run(
            [sys.executable, str(TOOL), *args],
            check=check,
            text=True,
            capture_output=True,
        )

    def begin(self, stage):
        self.run_tool("begin", "--file", str(self.state), "--stage", stage)

    def finish(self, stage, code):
        self.run_tool(
            "finish",
            "--file",
            str(self.state),
            "--stage",
            stage,
            "--exit-code",
            str(code),
        )

    def test_clean_lifecycle_is_an_exact_prefix(self):
        for stage in STAGES:
            self.begin(stage)
            self.finish(stage, 0)
        state = json.loads(self.state.read_text())
        self.assertEqual(STAGES, state["completed"])
        self.assertEqual("complete_clean", state["status"])
        self.assertFalse(state["resumed"])

    def test_failed_stage_retries_without_replaying_completed_prefix(self):
        self.begin("fixture")
        self.finish("fixture", 0)
        self.begin("host-preflight")
        self.finish("host-preflight", 17)

        replay = self.run_tool(
            "begin",
            "--file",
            str(self.state),
            "--stage",
            "fixture",
            check=False,
        )
        self.assertNotEqual(0, replay.returncode)
        self.assertIn("already complete", replay.stderr)

        retry_without_resume = self.run_tool(
            "begin",
            "--file",
            str(self.state),
            "--stage",
            "host-preflight",
            check=False,
        )
        self.assertNotEqual(0, retry_without_resume.returncode)
        self.assertIn("marked resumed", retry_without_resume.stderr)

        self.run_tool("mark-resumed", "--file", str(self.state), "--tree-id", TREE_ID)
        self.begin("host-preflight")
        self.finish("host-preflight", 0)
        for stage in STAGES[2:]:
            self.begin(stage)
            self.finish(stage, 0)
        state = json.loads(self.state.read_text())
        self.assertEqual("complete_after_resume", state["status"])
        attempts = [attempt for attempt in state["attempts"] if attempt["stage"] == "host-preflight"]
        self.assertEqual([17, 0], [attempt["exit_code"] for attempt in attempts])

    def test_crashed_running_attempt_is_closed_before_resume(self):
        self.begin("fixture")
        self.run_tool("mark-resumed", "--file", str(self.state), "--tree-id", TREE_ID)
        state = json.loads(self.state.read_text())
        self.assertEqual(255, state["attempts"][-1]["exit_code"])
        self.assertIsNotNone(state["attempts"][-1]["finished_at"])
        self.begin("fixture")
        self.finish("fixture", 0)

    def test_out_of_order_and_tampered_prefix_are_rejected(self):
        result = self.run_tool(
            "begin",
            "--file",
            str(self.state),
            "--stage",
            "el1",
            check=False,
        )
        self.assertNotEqual(0, result.returncode)
        self.assertIn("next stage is fixture", result.stderr)

        state = json.loads(self.state.read_text())
        state["completed"] = ["fixture", "el1"]
        self.state.write_text(json.dumps(state))
        result = self.run_tool("validate", "--file", str(self.state), check=False)
        self.assertNotEqual(0, result.returncode)
        self.assertIn("not an exact ordered prefix", result.stderr)

    def test_source_and_uuid_validation_fail_closed(self):
        mismatch = self.run_tool(
            "validate",
            "--file",
            str(self.state),
            "--source-sha",
            "c" * 40,
            check=False,
        )
        self.assertNotEqual(0, mismatch.returncode)
        self.assertIn("does not match checkout", mismatch.stderr)

        state = json.loads(self.state.read_text())
        state["enclave"]["uuid"] = "short"
        self.state.write_text(json.dumps(state))
        invalid = self.run_tool("validate", "--file", str(self.state), check=False)
        self.assertNotEqual(0, invalid.returncode)
        self.assertIn("invalid enclave UUID", invalid.stderr)

    def test_tree_id_tracks_untracked_content(self):
        repo = Path(self.temporary.name) / "repo"
        repo.mkdir()
        subprocess.run(["git", "init", "-q"], cwd=repo, check=True)
        subprocess.run(["git", "config", "user.email", "test@example.com"], cwd=repo, check=True)
        subprocess.run(["git", "config", "user.name", "Test"], cwd=repo, check=True)
        (repo / "tracked.txt").write_text("one\n")
        subprocess.run(["git", "add", "tracked.txt"], cwd=repo, check=True)
        subprocess.run(["git", "commit", "-qm", "initial"], cwd=repo, check=True)
        first = self.run_tool("tree-id", "--repo", str(repo)).stdout.strip()
        (repo / "untracked.txt").write_text("two\n")
        second = self.run_tool("tree-id", "--repo", str(repo)).stdout.strip()
        self.assertRegex(first, r"^[0-9a-f]{64}$")
        self.assertNotEqual(first, second)


if __name__ == "__main__":
    unittest.main()
