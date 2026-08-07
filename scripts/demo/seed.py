"""Seed run history for the demo recording, relative to now."""

import datetime as dt
import json
import os
import sys

state = sys.argv[1]
now = dt.datetime.now().astimezone()
os.makedirs(state, exist_ok=True)


def rec(run_id, name, status, minutes_ago, workspace="", error=""):
    return json.dumps(
        {
            "run_id": run_id,
            "automation": name,
            "trigger": "cron",
            "status": status,
            "at": (now - dt.timedelta(minutes=minutes_ago)).isoformat(),
            "workspace_id": workspace,
            "error": error,
        }
    )


lines = [
    rec("r1", "issue-triage", "scheduled", 190),
    rec("r1", "issue-triage", "running", 189, "w3"),
    rec("r1", "issue-triage", "done", 175, "w3"),
    rec("r2", "nightly-deps", "scheduled", 640),
    rec("r2", "nightly-deps", "running", 639, "w4"),
    rec("r2", "nightly-deps", "failed", 610, "w4", "prompt: agent_prompt_stalled"),
]
with open(f"{state}/history.jsonl", "w") as f:
    f.write("\n".join(lines) + "\n")
