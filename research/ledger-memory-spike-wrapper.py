# Reference: the spike wrapper validated by research/ledger-memory-spike-eval.md (8/8 agents).
# NOT production code — the ledger-memory plugin's Task 2 ports this with the four rev-5 deltas
# listed in docs/superpowers/plans/2026-08-15-ledger-memory-plugin.md.
#!/usr/bin/env python3
"""ledger-memory — SPIKE (throwaway). The only write path for agent memory.

Subcommands:
  save <name> -m "<hook>" [--evidence REF] [--no-evidence] [--body FILE]
  retract <name> -m "wrong because <why>"
  archive <name> [-m "<why>"]
  render
  drill <name>

Memory dir comes from $LEDGER_MEMORY_DIR. Ledger binary: $LEDGER_BIN or PATH.
Every mutating subcommand ends by re-rendering MEMORY.md (atomic).
"""
import json
import os
import re
import subprocess
import sys
import tempfile
from datetime import datetime, timezone

MEMDIR = os.environ.get("LEDGER_MEMORY_DIR", "")
LEDGER = os.environ.get("LEDGER_BIN", "ledger")
AUTHOR = os.environ.get("LEDGER_MEMORY_AS", "memory")
NAG_LINES = 60


def die(msg, code=1):
    print(f"ledger-memory: {msg}", file=sys.stderr)
    sys.exit(code)


def run(*args, ok_codes=(0,)):
    try:
        p = subprocess.run([LEDGER, *args], cwd=MEMDIR, capture_output=True, text=True)
    except FileNotFoundError:
        die("the `ledger` binary is missing. Tell the user and STOP — do not fall "
            "back to writing files. Install: curl -fsSL https://github.com/"
            "prime-radiant-inc/ledger/releases/latest/download/install.sh | bash")
    doc = None
    if p.stdout.strip():
        try:
            doc = json.loads(p.stdout.strip())
        except ValueError:
            pass
    err = None
    if p.returncode not in ok_codes:
        try:
            err = json.loads(p.stderr.strip().splitlines()[-1])
        except Exception:
            err = {"error": "unknown", "message": p.stderr.strip()}
    return p.returncode, doc, err


def memory_md_path():
    return os.path.join(MEMDIR, "MEMORY.md")


def recorded_head():
    try:
        with open(memory_md_path()) as f:
            m = re.search(r"store head: ([0-9a-f]{6,})", f.read())
            return m.group(1) if m else None
    except OSError:
        return None


def bootstrap():
    """Shared preamble: three-state rule (spec rev 4)."""
    if not MEMDIR:
        die("LEDGER_MEMORY_DIR is not set — it names the memory directory")
    if not os.path.isdir(MEMDIR):
        die(f"memory dir missing: {MEMDIR}")
    store = os.path.join(MEMDIR, ".ledger.git")
    if not os.path.isdir(store):
        _, _, err = run("init")
        if err:
            die(f"init failed: {err.get('message')}")
        _create()
        return
    rc, doc, err = run("ls", "--all", ok_codes=(0, 1, 4))
    slugs = [l.get("slug") for l in (doc or {}).get("ledgers", [])]
    if "memory" in slugs:
        return
    claimed = recorded_head()
    if err or claimed:
        detail = f"; MEMORY.md records head {claimed}" if claimed else ""
        die("store may be damaged (a .ledger.git exists but the memory ledger is "
            f"unreadable{detail}). STOP and tell the human. Never create a new "
            "ledger over an existing store.", 2)
    _create()


def _create():
    _, _, err = run("create", "memory",
                    "--scope", f"agent memory ({os.path.basename(MEMDIR.rstrip('/')) or 'project'})",
                    "--field", "status=current,retracted,archived", "--as", AUTHOR)
    if err:
        die(f"create failed: {err.get('message')}")


def spine_rows():
    _, doc, err = run("show", ok_codes=(0, 4))
    if err:
        return None, []
    doc = doc or {}
    return doc.get("head"), [r for r in doc.get("rows", []) if r.get("field") == "status"]


def chain_events():
    _, doc, err = run("tail", "--raw", "-n", "100000")
    if err:
        die(f"reading the chain failed: {err.get('message')}")
    return (doc or {}).get("events", [])


def key_history(events):
    """Group set-events by key: ever-retracted flag + last retraction text."""
    hist = {}
    for e in events:
        if e.get("type") != "set" or "status" not in e.get("fields", {}):
            continue
        h = hist.setdefault(e.get("key"), {"retraction": None})
        if e["fields"]["status"] == "retracted":
            h["retraction"] = sanitize(e.get("text", ""))
    return hist


def latest_bodies():
    _, doc, err = run("notes", "-k", "body", "-n", "100000", ok_codes=(0, 4))
    latest = {}
    for n in (doc or {}).get("notes", []):
        latest[n.get("key")] = n.get("text", "")  # notes arrive oldest-first; keep last
    return latest


def sanitize(s):
    s = re.sub(r"[\x00-\x1f\x7f]", " ", s or "")
    s = re.sub(r"\s+", " ", s).strip()
    return s[:300]


def age(ts):
    try:
        t = datetime.strptime(ts, "%Y-%m-%dT%H:%M:%S").replace(tzinfo=timezone.utc)
        d = (datetime.now(timezone.utc) - t).days
        return f"{d}d" if d else "today"
    except (ValueError, TypeError):
        return "?"


def render():
    bootstrap()
    head, rows = spine_rows()
    events = chain_events()
    hist = key_history(events)
    bodies = latest_bodies()

    wrapper = os.path.abspath(__file__)
    header = [
        "<!-- GENERATED from the memory ledger. NEVER edit this file. NEVER write",
        "     memory as files in this directory: file writes here are LOST. -->",
        "# Memory (ledger-backed)",
        "",
        "Write memory ONLY through the wrapper (paste-ready; it re-renders this file):",
        "```",
        f"export LEDGER_MEMORY_DIR={MEMDIR}",
        *([f"export LEDGER_BIN={LEDGER}"] if LEDGER != "ledger" else []),
        f"{wrapper} save <kebab-name> -m \"<one-line fact>\" [--evidence file:...] [--body <file>]",
        f"{wrapper} retract <kebab-name> -m \"wrong because <why>\"",
        f"{wrapper} archive <kebab-name>",
        f"{wrapper} drill <kebab-name>     # full detail for one memory",
        "```",
        "A save is not finished until this file has re-rendered (the wrapper does it).",
        "If `ledger` or the wrapper is missing or erroring: tell the user and STOP.",
        f"store head: {head or 'none'}",
        "",
    ]
    current, retracted = [], []
    for r in sorted(rows, key=lambda r: r.get("ts", "")):
        key, val = r.get("key"), r.get("value")
        hook = sanitize(r.get("note", ""))
        ev = f", evidence: {r['evidence'][0]}" if r.get("evidence") else ""
        if val == "current":
            scar = ""
            if hist.get(key, {}).get("retraction"):
                scar = f" [previously retracted: {hist[key]['retraction']}]"
            current.append(f"- **{key}** — {hook} ({age(r.get('ts'))}, by {r.get('by')}{ev}){scar}")
        elif val == "retracted":
            retracted.append(f"- ~~{key}~~ retracted: {hook} ({age(r.get('ts'))})")

    lines = header
    lines += ["## Facts", ""] + (current or ["(none yet)"])
    if retracted:
        lines += ["", "## Retracted (kept as vaccines — do not re-derive these)", ""] + retracted
    body_text = "\n".join(lines)
    if len(lines) > NAG_LINES:
        linked = set(re.findall(r"\[\[([a-z0-9-]+)\]\]",
                                body_text + " " + " ".join(bodies.values())))
        cands = [r.get("key") for r in sorted(rows, key=lambda r: r.get("ts", ""))
                 if r.get("value") == "current" and r.get("key") not in linked][:3]
        lines += ["", f"note: curation due ({len(lines)} lines). Archive candidates "
                      f"(oldest, unlinked): {', '.join(cands) or 'none'}"]

    fd, tmp = tempfile.mkstemp(dir=MEMDIR, prefix=".memory-render-")
    with os.fdopen(fd, "w") as f:
        f.write("\n".join(lines) + "\n")
    os.replace(tmp, memory_md_path())
    return head


def cmd_save(name, msg, evidence, no_evidence, body):
    bootstrap()
    _, rows = spine_rows()
    prior = next((r for r in rows if r.get("key") == name), None)
    hist = key_history(chain_events())
    ev_args = []
    if evidence:
        ev_args = ["--evidence", evidence]
    elif prior and prior.get("evidence") and not no_evidence:
        ev_args = ["--evidence", prior["evidence"][0]]  # carry forward (spec rev 4)
    _, doc, err = run("set", name, "status=current", "-m", msg, "--as", AUTHOR, *ev_args)
    if err:
        die(f"save failed: {err.get('message')} (hint: {err.get('hint', '')})")
    if body:
        _, _, nerr = run("note", "-k", "body", "--key", name, "--from-file", body, "--as", AUTHOR)
        if nerr:
            die(f"saved, but body note failed: {nerr.get('message')}")
    render()
    out = {"ok": True, "saved": name, "id": (doc or {}).get("id")}
    if prior:
        note = f"replaced: \"{sanitize(prior.get('note',''))}\" ({age(prior.get('ts'))}, was {prior.get('value')})"
        if prior.get("value") == "retracted" or hist.get(name, {}).get("retraction"):
            note += f" — this key carries a retraction on record: \"{hist.get(name, {}).get('retraction') or sanitize(prior.get('note',''))}\". If your new fact re-asserts the retracted claim, retract it again."
        out["replaced"] = note
    print(json.dumps(out, indent=1))


def cmd_status_flip(name, status, msg):
    bootstrap()
    _, rows = spine_rows()
    prior = next((r for r in rows if r.get("key") == name), None)
    if not prior:
        die(f"no memory named '{name}' (drill the projection's names, or save it first)", 4)
    ev_args = ["--evidence", prior["evidence"][0]] if prior.get("evidence") else []
    _, doc, err = run("set", name, f"status={status}", "-m", msg, "--as", AUTHOR, *ev_args)
    if err:
        die(f"{status} failed: {err.get('message')}")
    render()
    print(json.dumps({"ok": True, status: name, "id": (doc or {}).get("id")}, indent=1))


def cmd_drill(name):
    bootstrap()
    _, rows = spine_rows()
    row = next((r for r in rows if r.get("key") == name), None)
    if not row:
        die(f"no memory named '{name}'", 4)
    events = [e for e in chain_events() if e.get("key") == name and e.get("type") == "set"]
    _, notes_doc, _ = run("notes", "-k", "body", "--key", name, "--latest", ok_codes=(0, 4))
    body = ((notes_doc or {}).get("notes") or [{}])[-1].get("text")
    print(json.dumps({"ok": True, "memory": name, "state": row,
                      "history": [{"ts": e.get("ts"), "status": e["fields"]["status"],
                                   "note": e.get("text"), "by": e.get("author"),
                                   "evidence": e.get("evidence")} for e in events],
                      "body": body}, indent=1))


def main():
    args = sys.argv[1:]
    if not args or args[0] in ("-h", "--help"):
        print(__doc__)
        return
    cmd, rest = args[0], args[1:]

    def flag(name, boolean=False):
        if name in rest:
            i = rest.index(name)
            rest.pop(i)
            return True if boolean else rest.pop(i)
        return False if boolean else None

    if cmd == "render":
        render()
        print(json.dumps({"ok": True, "rendered": memory_md_path()}, indent=1))
    elif cmd == "save":
        msg = flag("-m")
        evidence = flag("--evidence")
        no_ev = flag("--no-evidence", boolean=True)
        body = flag("--body")
        if not rest or not msg:
            die("usage: ledger-memory save <name> -m \"<hook>\" [--evidence REF] [--body FILE]", 4)
        cmd_save(rest[0], msg, evidence, no_ev, body)
    elif cmd == "retract":
        msg = flag("-m")
        if not rest or not msg:
            die("usage: ledger-memory retract <name> -m \"wrong because <why>\"", 4)
        cmd_status_flip(rest[0], "retracted", msg)
    elif cmd == "archive":
        msg = flag("-m") or "archived (stale, not wrong)"
        if not rest:
            die("usage: ledger-memory archive <name> [-m \"<why>\"]", 4)
        cmd_status_flip(rest[0], "archived", msg)
    elif cmd == "drill":
        if not rest:
            die("usage: ledger-memory drill <name>", 4)
        cmd_drill(rest[0])
    else:
        die(f"unknown subcommand '{cmd}' (save|retract|archive|render|drill)", 4)


if __name__ == "__main__":
    main()
