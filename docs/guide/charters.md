---
title: "Charters: how they work and how to write your own"
status: accepted
stage: CURRENT
tags: [tillr, guide, charters, definitions]
summary: "What a tillr charter is, what the default git-landing charter declares, and a step-by-step worked example of a custom charter that finishes items through a hook without landing in Git."
---

# Charters: how they work and how to write your own

A **charter** is the layer that turns `tillr`'s core queue and workflow engine into one specific use. The core handles SQLite storage, items, criteria, dependency edges, lane leases, workflow graphs, and event logging. A charter supplies the rules that vary from one use to another: what states an item can have, which state means ready for dispatch, which states count as finished, which state transitions are allowed, what prerequisites an item needs before it starts, and whether an item ends by landing a Git commit or by calling a hook program.

In the vocabulary, this layer is called a **charter**; in the Go code (`tools/tillr/definition/`) and CLI flags (`--definition`), it is called a **definition**. When you initialize a store with `tillr store init --store .tillr/state.db` and omit `--definition`, `tillr` uses the embedded **`git-landing`** charter (`tools/tillr/definition/landing.json`).

---

## 1. What the default `git-landing` charter declares

The embedded `git-landing` charter configures `tillr` for landing changes in a Git repository:

- **States:** `not-started`, `blocked`, `in-queue`, `landed`, `resolved-elsewhere`, and `closed`.
- **Role states:** `ready` is `in-queue`, `blocked` is `blocked`, and `closed` is `closed`.
- **Finished states:** `landed`, `resolved-elsewhere`, and `closed`.
- **Transitions:** items can move between unfinished states, enter any finished state, reopen from `closed` to an unfinished state, or move from `landed` back to `in-queue` (for an automatically reverted landing). `resolved-elsewhere` has no outgoing transition.
- **Prerequisites:** `design-document`, `design-questions`, and `owner-clean`. Before dispatching an item, the planner checks that its design document exists, has no unanswered questions, and that the owner checkout has no dirty paths blocking a landing.
- **Completion:** `completes` is `"landing"`, `retention` is `"keep"`, and `hooks` is `null`.

Every store records its active charter in SQLite (`meta` key `store.definition`). You can inspect it at any time with:

```console
$ tillr store definition --store .tillr/state.db
```

You can also replace a store's charter later with `tillr store definition --store .tillr/state.db --set revised.json`. That command validates the JSON first, checks that no existing item sits in a state the new charter removes, and records a `store.definition.set` event atomically.

---

## 2. When to write a custom charter

Write a custom charter when you want `tillr`'s queue, isolated lanes, workflow graphs, and append-only event log for work that **does not land a Git commit** — for example, verifying findings, triaging alerts, or running scheduled checks.

When a charter sets `"completes": "hook"` and names a hook program under `"hooks"`, `tillr drain loop` skips Git repository startup checks, item block syncing, trunk history, Git worktree staging, the gate, and the lander. Instead, once an item's workflow finishes, the loop calls your hook program's `complete-item` subcommand, which records criterion verdicts in the store and returns the finished state to move the item into.

> **What is built vs. not yet built.** The charter JSON format, `tillr store init --definition`, `tillr store definition --set`, and `completes: "hook"` with `complete-item` are built and working today. Moving the remaining Git-landing Go code inside `tillr-drain` behind the charter's hook, calling the `detail` and `after-completion` hooks, and drawing observatory views from the charter's finished states are designed in [The core and the `git-landing` charter](../design/tillr-core-and-git-landing.md#82-what-is-built-and-what-is-not) and are **not yet built**.

---

## 3. Worked example: a `file-check` charter from nothing

This example builds a complete, runnable custom charter in an empty directory with **no Git repository** and **no AI model**. It uses a one-stage command workflow and a small Python hook that checks whether `HELLO.txt` exists in the root directory, records a criterion verdict, and finishes the item as `verified` or `rejected`.

Create an empty directory and move into it:

```console
$ mkdir charter-example && cd charter-example
```

### Step 1: Write the charter file (`charter.json`)

Create `charter.json` with five states (`queued`, `blocked`, `closed`, `verified`, `rejected`), no Git prerequisites (`"prerequisites": []`), `"completes": "hook"`, and a `hooks` object pointing to `python3 hook.py`:

```json
{
  "definition": "file-check",
  "version": 1,
  "states": ["queued", "blocked", "closed", "verified", "rejected"],
  "ready": "queued",
  "blocked": "blocked",
  "closed": "closed",
  "finished": ["verified", "rejected", "closed"],
  "transitions": [
    {"from": "queued", "to": "blocked"},
    {"from": "queued", "to": "verified"},
    {"from": "queued", "to": "rejected"},
    {"from": "queued", "to": "closed"},
    {"from": "blocked", "to": "queued"},
    {"from": "blocked", "to": "closed"},
    {"from": "closed", "to": "queued"}
  ],
  "prerequisites": [],
  "completes": "hook",
  "retention": "keep",
  "brief_template": "brief.md",
  "default_criteria": [
    {
      "criterion_id": "c1",
      "text": "HELLO.txt exists in the root directory",
      "claim_class": "verification-only"
    }
  ],
  "hooks": {
    "argv": ["python3", "hook.py"],
    "names": ["complete-item"]
  }
}
```

Note three details:

- `brief_template` names a template file (`brief.md`) used when staging a lane in a plain directory.
- `default_criteria` is optional: when declared, any item applied without its own `criteria` receives these criteria automatically. Items that supply their own `criteria` keep their own.
- `hooks.argv` is executed directly without a shell, and `hooks.names` lists the hook operations this program answers (`complete-item`).

### Step 2: Write the brief template and workflow files

Create `brief.md`:

```markdown
# Task {{ITEM_ID}}

{{TITLE}}

## Task

{{TASK}}

## Criteria

{{CRITERION_1}}
```

Create `config/tillr/workflows/check-file@v1.json` with one `command` stage:

```json
{
  "name": "check-file",
  "version": 1,
  "stages": [
    {
      "id": "check",
      "kind": "command",
      "command_ref": "check",
      "access": "read_only"
    }
  ],
  "edges": []
}
```

Register the `check` command in `config/tillr/commands.json` (use the absolute path to `python3` from `which python3` in your environment):

```json
{
  "check": {
    "argv": ["/mise/shims/python3", "-c", "print('checked')"],
    "flags": {}
  }
}
```

The drain loop also reads `config/tillr/backend-policy.json` at startup even when the workflow only runs command stages. Create a minimal stub adapter `stub-adapter.sh` (`chmod +x stub-adapter.sh`) and `config/tillr/backend-policy.json` pointing to its absolute path:

```json
{
  "default_backend": "stub",
  "seats": {
    "build": "stub"
  },
  "backends": {
    "stub": {
      "adapter": "/path/to/charter-example/stub-adapter.sh",
      "model": "none"
    }
  }
}
```

### Step 3: Write the hook program (`hook.py`)

A hook program must answer `describe` (printing the flags it accepts for each hook name) and each hook named in `hooks.names`. For `complete-item`, it receives `--item`, `--run`, `--store`, `--root`, and `--artifact-dir` (the run's artifact directory where the brief and run outputs were written), must record a verdict for every criterion on the item using `tillr item verdict record` (binding the verdict to the SHA-256 hash of each criterion's text), and prints one JSON object naming `state`, `reason`, and `resolves_via`:

```python
import argparse, datetime, hashlib, json, subprocess, sys
from pathlib import Path

FLAGS = ["--item", "--run", "--store", "--root", "--artifact-dir"]
if sys.argv[1:] == ["describe"]:
    print(json.dumps({"hooks": {"complete-item": FLAGS}}))
    sys.exit(0)

parser = argparse.ArgumentParser()
parser.add_argument("hook", choices=["complete-item"])
for flag in FLAGS:
    parser.add_argument(flag, required=True)
args = parser.parse_args()

root = Path(args.root)
exists = (root / "HELLO.txt").is_file()
state = "verified" if exists else "rejected"
verdict_val = "done" if exists else "not-done"
reason = "HELLO.txt is present" if exists else "HELLO.txt is missing"

shown = json.loads(subprocess.check_output([
    "tillr", "item", "show", "--store", args.store, "--item", args.item,
]))["payload"]
now = datetime.datetime.now(datetime.timezone.utc).isoformat().replace("+00:00", "Z")

for c in shown["criteria"]:
    rec = {
        "record_version": 1,
        "kind": "item.verdict",
        "id": f"{args.run}-{c['criterion_id']}",
        "producer": {"system": "tillr", "utility": "file-check-hook", "version": "0.1.0"},
        "created_at": now,
        "payload": {
            "item_id": args.item,
            "criterion_id": c["criterion_id"],
            "criterion_hash": hashlib.sha256(c["text"].encode()).hexdigest(),
            "verdict": verdict_val,
            "claim_class": c["claim_class"],
            "reason": reason,
            "evidence": ["doc:charter.json"],
            "actor_kind": "mechanical",
            "actor": "file-check-hook",
        },
    }
    vpath = root / f"{args.item}-{c['criterion_id']}-verdict.json"
    vpath.write_text(json.dumps(rec) + "\n")
    subprocess.run([
        "tillr", "item", "verdict", "record", "--store", args.store,
        "--input", str(vpath), "--now", now,
    ], check=True, stdout=subprocess.DEVNULL)

print(json.dumps({"state": state, "reason": reason, "resolves_via": "doc:charter.json"}))
```

### Step 4: Initialize, file an item, and run the loop

Create `HELLO.txt`, an item record `item-1.json`, validate the workflow, initialize the store with `--definition charter.json`, apply the item, and run `tillr drain loop`:

```console
$ printf 'hello\n' > HELLO.txt
$ cat > item-1.json <<'JSON'
{
  "record_version": 1,
  "kind": "item.record",
  "id": "item-check-1",
  "producer": {"system": "tillr", "utility": "cli", "version": "0.1.0"},
  "created_at": "2026-11-25T00:00:00Z",
  "payload": {
    "item_id": "check-hello",
    "title": "Verify HELLO.txt exists",
    "state": "queued",
    "workflow": "check-file@v1",
    "criteria": [
      {
        "criterion_id": "c1",
        "text": "HELLO.txt exists in the root directory",
        "claim_class": "verification-only"
      }
    ]
  }
}
JSON
$ tillr watch lint . --charter charter.json --workflows config/tillr/workflows --policy config/tillr/backend-policy.json
$ tillr store init --store .tillr/state.db --definition charter.json
{"record_version":1,"kind":"store.report",...,"payload":{"action":"init","schema_version":6,...,"integrity":"ok"}}
$ tillr store definition --store .tillr/state.db
{"record_version":1,"kind":"store.definition",...,"payload":{"definition":"file-check","version":1,...,"default":false}}
$ tillr item apply --store .tillr/state.db --input item-1.json --now 2026-11-25T00:00:00Z
{"record_version":1,"kind":"item.record","id":"item-check-1",...,"payload":{"item_id":"check-hello",...,"state":"queued",...}}
$ tillr drain loop --root "$PWD" --store "$PWD/.tillr/state.db" --lanes 1 --exit-when-drained
...
loop: definition: file-check; skipped Git, item sync, trunk history, Git lane staging, rolling tier, roll and push
loop: policy in force ...: default=stub; build=stub
loop: tick 1 dispatch check-hello -> command on lane-1
...
loop: a full tick planned nothing dispatchable: 1 held item(s): check-hello (not-ready); 0 deferred item(s): 
...
loop: stopped after 2 tick(s): queue-drained — a full tick planned nothing dispatchable: 1 held item(s): check-hello (not-ready); 0 deferred item(s): 
loop: dispatched 1, landed 0, rejected 0, infra failures 0
```

Now inspect the finished item with `tillr item show`:

```console
$ tillr item show --store .tillr/state.db --item check-hello
{"record_version":1,"kind":"item.record","id":"item-check-hello",...,"payload":{...,"criteria":[{"criterion_id":"c1","text":"HELLO.txt exists in the root directory","state":"done","claim_class":"verification-only"}],...,"lifecycle_by":"definition:file-check","lifecycle_reason":"HELLO.txt is present","place":1,"resolves_via":"doc:charter.json","selected_workflow":"check-file@v1","state":"verified","title":"Verify HELLO.txt exists",...}}
```

The item moved from `queued` to `verified`, `lifecycle_by` records `definition:file-check`, `lifecycle_reason` records `"HELLO.txt is present"`, and criterion `c1` is marked `"done"`, all without touching Git.

---

## 4. Further reading

- [Definition package README](../../tools/tillr/definition/README.md) — every key, type, default, prerequisite name, and hook flag contract.
- [Recorded verdict example](../../tools/tillr/examples/recorded-verdict/README.md) — a fuller custom-charter example shipped in the repository with a `reproduce` command stage and an `investigate` agent stage.
- [The core and the `git-landing` charter](../design/tillr-core-and-git-landing.md) — the design splitting `tillr`'s core from charters, including what is built today and what units remain unbuilt.
