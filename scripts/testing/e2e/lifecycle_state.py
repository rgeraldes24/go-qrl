#!/usr/bin/env python3
"""Atomic, append-only checkpoint state for the local VM64 E2E lifecycle."""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import subprocess
import tempfile
from datetime import datetime, timezone
from pathlib import Path


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
SHA_RE = re.compile(r"^[0-9a-f]{40}$")
UUID_RE = re.compile(r"^[0-9a-f]{32}$")
TREE_ID_RE = re.compile(r"^[0-9a-f]{64}$")
STATUSES = {
    "running",
    "failed",
    "complete_clean",
    "complete_after_resume",
    "cleaned_after_failure",
}


def now() -> str:
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")


def load(path: Path) -> dict:
    try:
        state = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise SystemExit(f"invalid lifecycle state {path}: {exc}") from exc
    validate_state(state)
    return state


def validate_state(state: dict) -> None:
    if state.get("schema") != 1:
        raise SystemExit("lifecycle state schema must be 1")
    if not SHA_RE.fullmatch(str(state.get("source_sha", ""))):
        raise SystemExit("lifecycle state has an invalid source_sha")
    enclave = state.get("enclave")
    if not isinstance(enclave, dict) or not enclave.get("name"):
        raise SystemExit("lifecycle state has no enclave name")
    if not UUID_RE.fullmatch(str(enclave.get("uuid", ""))):
        raise SystemExit("lifecycle state has an invalid enclave UUID")
    if not isinstance(state.get("dump_dir"), str) or not state["dump_dir"]:
        raise SystemExit("lifecycle state has no dump directory")
    if state.get("status") not in STATUSES:
        raise SystemExit("lifecycle state has an invalid status")
    if not TREE_ID_RE.fullmatch(str(state.get("initial_tree_id", ""))):
        raise SystemExit("lifecycle state has an invalid initial tree ID")
    resume_tree_ids = state.get("resume_tree_ids")
    if not isinstance(resume_tree_ids, list) or any(
        not TREE_ID_RE.fullmatch(str(tree_id)) for tree_id in resume_tree_ids
    ):
        raise SystemExit("lifecycle state has invalid resume tree IDs")
    completed = state.get("completed")
    if not isinstance(completed, list) or completed != STAGES[: len(completed)]:
        raise SystemExit("completed lifecycle stages are not an exact ordered prefix")
    if len(completed) > len(STAGES):
        raise SystemExit("lifecycle state has too many completed stages")
    attempts = state.get("attempts")
    if not isinstance(attempts, list):
        raise SystemExit("lifecycle state attempts must be a list")
    expected_stage_index = 0
    for attempt in attempts:
        if not isinstance(attempt, dict) or attempt.get("stage") not in STAGES:
            raise SystemExit("lifecycle state contains an invalid attempt")
        if expected_stage_index >= len(STAGES) or attempt["stage"] != STAGES[expected_stage_index]:
            raise SystemExit("lifecycle state attempts are out of stage order")
        if not isinstance(attempt.get("attempt"), int) or attempt["attempt"] < 1:
            raise SystemExit("lifecycle state contains an invalid attempt number")
        exit_code = attempt.get("exit_code")
        finished_at = attempt.get("finished_at")
        if (finished_at is None) != (exit_code is None):
            raise SystemExit("lifecycle state attempt completion fields disagree")
        if exit_code is not None and not isinstance(exit_code, int):
            raise SystemExit("lifecycle state contains an invalid exit code")
        if exit_code == 0:
            expected_stage_index += 1
    unfinished = [attempt for attempt in attempts if attempt.get("finished_at") is None]
    if len(unfinished) > 1 or (unfinished and attempts[-1] is not unfinished[0]):
        raise SystemExit("lifecycle state has an invalid running attempt sequence")
    if expected_stage_index != len(completed):
        raise SystemExit("lifecycle completed stages do not match successful attempts")
    current_stage = state.get("current_stage")
    if current_stage is not None and current_stage not in STAGES:
        raise SystemExit("lifecycle state has an invalid current stage")
    if unfinished and (state["status"] != "running" or current_stage != unfinished[0]["stage"]):
        raise SystemExit("lifecycle running attempt does not match current stage")
    if state["status"] == "failed":
        if not attempts or attempts[-1].get("exit_code") in (None, 0):
            raise SystemExit("failed lifecycle state has no failed attempt")
        if current_stage != attempts[-1]["stage"]:
            raise SystemExit("failed lifecycle state does not identify its failed stage")
    if state["status"] in {"complete_clean", "complete_after_resume"}:
        if completed != STAGES or current_stage is not None or unfinished:
            raise SystemExit("successful lifecycle state is not fully complete")


def save(path: Path, state: dict) -> None:
    validate_state(state)
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix=path.name + ".", dir=path.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            json.dump(state, stream, indent=2, sort_keys=True)
            stream.write("\n")
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
        fsync_directory(path.parent)
    finally:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass


def save_new(path: Path, state: dict) -> None:
    """Publish a complete initial state without ever replacing an existing file."""
    validate_state(state)
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix=path.name + ".", dir=path.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            json.dump(state, stream, indent=2, sort_keys=True)
            stream.write("\n")
            stream.flush()
            os.fsync(stream.fileno())
        try:
            os.link(temporary, path)
        except FileExistsError as exc:
            raise SystemExit(f"refusing to replace existing lifecycle state {path}") from exc
        fsync_directory(path.parent)
    finally:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass


def fsync_directory(path: Path) -> None:
    descriptor = os.open(path, os.O_RDONLY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def require_next_stage(state: dict, stage: str) -> None:
    if stage not in STAGES:
        raise SystemExit(f"unknown lifecycle stage {stage!r}")
    completed = state["completed"]
    if stage in completed:
        raise SystemExit(f"lifecycle stage {stage} is already complete")
    expected = STAGES[len(completed)]
    if stage != expected:
        raise SystemExit(f"lifecycle stage {stage} is out of order; next stage is {expected}")


def command_init(args: argparse.Namespace) -> None:
    path = Path(args.file)
    if path.exists():
        raise SystemExit(f"refusing to replace existing lifecycle state {path}")
    if not SHA_RE.fullmatch(args.source_sha):
        raise SystemExit("source SHA must be 40 lowercase hexadecimal characters")
    if not UUID_RE.fullmatch(args.enclave_uuid):
        raise SystemExit("enclave UUID must be 32 lowercase hexadecimal characters")
    timestamp = now()
    state = {
        "schema": 1,
        "source_sha": args.source_sha,
        "enclave": {"name": args.enclave_name, "uuid": args.enclave_uuid},
        "dump_dir": args.dump_dir,
        "initial_tree_id": args.tree_id,
        "resume_tree_ids": [],
        "status": "running",
        "current_stage": None,
        "completed": [],
        "attempts": [],
        "resumed": False,
        "created_at": timestamp,
        "updated_at": timestamp,
    }
    save_new(path, state)


def command_validate(args: argparse.Namespace) -> None:
    state = load(Path(args.file))
    if args.source_sha and state["source_sha"] != args.source_sha:
        raise SystemExit(
            f"lifecycle state source {state['source_sha']} does not match checkout {args.source_sha}"
        )
    if state["status"] in {"complete_clean", "complete_after_resume", "cleaned_after_failure"}:
        raise SystemExit(f"lifecycle state is terminal: {state['status']}")


def command_get(args: argparse.Namespace) -> None:
    state = load(Path(args.file))
    fields = {
        "source_sha": state["source_sha"],
        "enclave_name": state["enclave"]["name"],
        "enclave_uuid": state["enclave"]["uuid"],
        "dump_dir": state["dump_dir"],
        "status": state["status"],
        "current_stage": state.get("current_stage") or "",
    }
    if args.field not in fields:
        raise SystemExit(f"unknown lifecycle state field {args.field!r}")
    print(fields[args.field])


def command_is_complete(args: argparse.Namespace) -> None:
    state = load(Path(args.file))
    if args.stage not in state["completed"]:
        # Reserve a distinct status for the expected "not complete yet" case;
        # malformed state and all other failures use the ordinary nonzero path.
        raise SystemExit(10)


def command_attempt_count(args: argparse.Namespace) -> None:
    state = load(Path(args.file))
    print(sum(1 for attempt in state["attempts"] if attempt["stage"] == args.stage))


def command_begin(args: argparse.Namespace) -> None:
    path = Path(args.file)
    state = load(path)
    require_next_stage(state, args.stage)
    if state["status"] == "failed":
        raise SystemExit("failed lifecycle state must be marked resumed before retry")
    if state["attempts"] and state["attempts"][-1]["finished_at"] is None:
        raise SystemExit("running lifecycle attempt must be marked resumed before retry")
    timestamp = now()
    attempt_number = 1 + sum(
        1 for attempt in state["attempts"] if attempt["stage"] == args.stage
    )
    state["attempts"].append(
        {
            "stage": args.stage,
            "attempt": attempt_number,
            "started_at": timestamp,
            "finished_at": None,
            "exit_code": None,
        }
    )
    state["status"] = "running"
    state["current_stage"] = args.stage
    state["updated_at"] = timestamp
    save(path, state)


def command_finish(args: argparse.Namespace) -> None:
    path = Path(args.file)
    state = load(path)
    require_next_stage(state, args.stage)
    if not state["attempts"]:
        raise SystemExit(f"lifecycle stage {args.stage} has no running attempt")
    attempt = state["attempts"][-1]
    if attempt["stage"] != args.stage or attempt["finished_at"] is not None:
        raise SystemExit(f"lifecycle stage {args.stage} has no current running attempt")
    timestamp = now()
    attempt["finished_at"] = timestamp
    attempt["exit_code"] = args.exit_code
    state["updated_at"] = timestamp
    if args.exit_code == 0:
        state["completed"].append(args.stage)
        state["current_stage"] = None
        if args.stage == "cleanup":
            retried = any(
                attempt.get("exit_code") not in (None, 0) for attempt in state["attempts"]
            ) or any(
                sum(1 for attempt in state["attempts"] if attempt["stage"] == stage) > 1
                for stage in STAGES
            )
            state["status"] = "complete_after_resume" if state.get("resumed") or retried else "complete_clean"
        else:
            state["status"] = "running"
    else:
        state["status"] = "failed"
        state["current_stage"] = args.stage
    save(path, state)


def command_mark_resumed(args: argparse.Namespace) -> None:
    path = Path(args.file)
    state = load(path)
    timestamp = now()
    if state["attempts"] and state["attempts"][-1]["finished_at"] is None:
        state["attempts"][-1]["finished_at"] = timestamp
        state["attempts"][-1]["exit_code"] = 255
    state["resumed"] = True
    if not TREE_ID_RE.fullmatch(args.tree_id):
        raise SystemExit("resume tree ID must be 64 lowercase hexadecimal characters")
    state["resume_tree_ids"].append(args.tree_id)
    state["status"] = "running"
    state["current_stage"] = None
    state["updated_at"] = timestamp
    save(path, state)


def command_mark_cleaned(args: argparse.Namespace) -> None:
    path = Path(args.file)
    state = load(path)
    if state["status"] in {"complete_clean", "complete_after_resume", "cleaned_after_failure"}:
        raise SystemExit(f"cannot mark terminal lifecycle state cleaned: {state['status']}")
    state["status"] = "cleaned_after_failure"
    state["updated_at"] = now()
    save(path, state)


def command_tree_id(args: argparse.Namespace) -> None:
    repo = Path(args.repo).resolve()
    try:
        head = subprocess.run(
            ["git", "rev-parse", "HEAD"],
            cwd=repo,
            check=True,
            capture_output=True,
        ).stdout
        status = subprocess.run(
            ["git", "status", "--porcelain=v1", "-z", "--untracked-files=all"],
            cwd=repo,
            check=True,
            capture_output=True,
        ).stdout
        diff = subprocess.run(
            ["git", "diff", "--binary", "HEAD"],
            cwd=repo,
            check=True,
            capture_output=True,
        ).stdout
        untracked = subprocess.run(
            ["git", "ls-files", "--others", "--exclude-standard", "-z"],
            cwd=repo,
            check=True,
            capture_output=True,
        ).stdout.split(b"\0")
    except subprocess.CalledProcessError as exc:
        raise SystemExit("could not fingerprint the Git working tree") from exc
    digest = hashlib.sha256()
    for label, payload in ((b"HEAD\0", head), (b"STATUS\0", status), (b"DIFF\0", diff)):
        digest.update(label)
        digest.update(payload)
    for raw_path in sorted(path for path in untracked if path):
        relative = os.fsdecode(raw_path)
        full_path = repo / relative
        digest.update(b"UNTRACKED\0")
        digest.update(raw_path)
        digest.update(b"\0")
        if full_path.is_symlink():
            digest.update(b"SYMLINK\0")
            digest.update(os.fsencode(os.readlink(full_path)))
        else:
            digest.update(full_path.read_bytes())
    print(digest.hexdigest())


def parser() -> argparse.ArgumentParser:
    result = argparse.ArgumentParser()
    subparsers = result.add_subparsers(dest="command", required=True)

    init = subparsers.add_parser("init")
    init.add_argument("--file", required=True)
    init.add_argument("--source-sha", required=True)
    init.add_argument("--enclave-name", required=True)
    init.add_argument("--enclave-uuid", required=True)
    init.add_argument("--dump-dir", required=True)
    init.add_argument("--tree-id", required=True)
    init.set_defaults(handler=command_init)

    validate = subparsers.add_parser("validate")
    validate.add_argument("--file", required=True)
    validate.add_argument("--source-sha")
    validate.set_defaults(handler=command_validate)

    get = subparsers.add_parser("get")
    get.add_argument("--file", required=True)
    get.add_argument("--field", required=True)
    get.set_defaults(handler=command_get)

    for name, handler in (
        ("is-complete", command_is_complete),
        ("attempt-count", command_attempt_count),
        ("begin", command_begin),
    ):
        command = subparsers.add_parser(name)
        command.add_argument("--file", required=True)
        command.add_argument("--stage", required=True, choices=STAGES)
        command.set_defaults(handler=handler)

    finish = subparsers.add_parser("finish")
    finish.add_argument("--file", required=True)
    finish.add_argument("--stage", required=True, choices=STAGES)
    finish.add_argument("--exit-code", required=True, type=int)
    finish.set_defaults(handler=command_finish)

    for name, handler in (
        ("mark-cleaned", command_mark_cleaned),
    ):
        command = subparsers.add_parser(name)
        command.add_argument("--file", required=True)
        command.set_defaults(handler=handler)

    mark_resumed = subparsers.add_parser("mark-resumed")
    mark_resumed.add_argument("--file", required=True)
    mark_resumed.add_argument("--tree-id", required=True)
    mark_resumed.set_defaults(handler=command_mark_resumed)

    tree_id = subparsers.add_parser("tree-id")
    tree_id.add_argument("--repo", required=True)
    tree_id.set_defaults(handler=command_tree_id)
    return result


def main() -> None:
    args = parser().parse_args()
    args.handler(args)


if __name__ == "__main__":
    main()
