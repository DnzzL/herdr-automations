"""Trim a cast to the demo itself: no terminal setup, no shell exit."""

import json
import sys

path, start_marker, end_marker, tail = (
    sys.argv[1],
    sys.argv[2],
    sys.argv[3],
    float(sys.argv[4]),
)
lines = open(path).read().splitlines()
header, events = lines[0], [json.loads(l) for l in lines[1:]]

first = next(i for i, e in enumerate(events) if start_marker in e[2])
last = max(i for i, e in enumerate(events) if end_marker in e[2])
events = events[first : last + 1]

events[0][0] = 0.4
events[-1][0] = max(events[-1][0], 0.2)
events.append([tail, "o", ""])  # hold the final screen before the loop restarts

open(path, "w").write("\n".join([header] + [json.dumps(e) for e in events]) + "\n")
print(f"events={len(events)} duration={sum(e[0] for e in events):.1f}s")
