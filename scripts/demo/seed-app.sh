#!/bin/sh
# Seeds the demo automations and run history the README's app screenshots
# show, under $DEMO (default /tmp/herdr-app-demo). Neutral names and paths:
# nothing from the recording machine reaches the picture.
set -eu
D="$(cd "$(dirname "$0")" && pwd)"
BIN="${BIN:-$D/../../bin/herdr-automations}"
DEMO="${DEMO:-/tmp/herdr-app-demo}"
rm -rf "$DEMO" && mkdir -p "$DEMO/cfg" "$DEMO/state" /tmp/myapp
export HERDR_PLUGIN_CONFIG_DIR="$DEMO/cfg" HERDR_PLUGIN_STATE_DIR="$DEMO/state"

put() { printf '%s' "$1" | "$BIN" put >/dev/null; }
printf 'backend: hc\nautomations: []\n' > "$DEMO/cfg/automations.yaml"
put '{"name":"issue-triage","cron":"0 9 * * 1-5","repo":"/tmp/myapp","workspace":"worktree","agent":"claude","model":"sonnet","prompt":"Triage new GitHub issues: label them, close duplicates, draft replies for the ones needing more info."}'
put '{"name":"nightly-deps","cron":"0 9 * * *","repo":"/tmp/myapp","workspace":"worktree","agent":"codex","prompt":"Bump outdated dependencies, run the tests, open a PR if they pass."}'
put '{"name":"flaky-hunt","cron":"0 9 * * 1","repo":"/tmp/myapp","workspace":"root","agent":"claude","model":"opus","prompt":"Find the flakiest test of the week in CI and fix its root cause."}'
put '{"name":"friday-digest","cron":"0 17 * * 5","repo":"/tmp/myapp","workspace":"root","agent":"pi","prompt":"Write the week digest: merged PRs, open questions, what slipped.","disabled":true}'

# Runs: started/ended relative to now, a few unread for the inbox.
python3 - "$DEMO/state/history.jsonl" <<'PY'
import json, sys, datetime as dt
now = dt.datetime.now().astimezone()
def iso(m): return (now - dt.timedelta(minutes=m)).isoformat()
runs = [
  ("issue-triage", "done", 14, 9, None, False),
  ("nightly-deps", "failed", 190, 186, "the agent exited with code 1 before finishing", False),
  ("issue-triage", "done", 1454, 1447, None, True),
  ("flaky-hunt", "done", 4330, 4291, None, True),
  ("nightly-deps", "done", 1630, 1612, None, True),
]
with open(sys.argv[1], "w") as f:
  # Oldest first: history is read in file order, the last record wins.
  for i, (name, status, start, end, err, seen) in sorted(enumerate(runs), key=lambda r: -r[1][2]):
    rid = f"{name}-{i}"
    f.write(json.dumps({"run_id": rid, "automation": name, "status": "running", "at": iso(start), "trigger": "cron"}) + "\n")
    rec = {"run_id": rid, "automation": name, "status": status, "at": iso(end), "trigger": "cron"}
    if err: rec["error"] = err
    f.write(json.dumps(rec) + "\n")
    if seen: f.write(json.dumps({**rec, "seen": True}) + "\n")
PY
# A live scheduler, as the panel checks for one: any running pid will do.
echo "${SCHEDULER_PID:-$$}" > "$DEMO/state/daemon.pid"
echo "$DEMO"
