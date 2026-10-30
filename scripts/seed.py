"""Seed a throwaway Wada data folder with many folders, chats and messages,
to see how the window holds up with them. `make demo` seeds a temporary folder
and runs the app on it.

Usage: python3 -I scripts/seed.py [--heavy] ROOT

--heavy adds what real use accumulates and the plain seed leaves out: session
change baselines (12 files of 64 KB per chat), image attachments (one
5120 x 2880 screenshot in the longest chat, 1600 x 1000 images in every fourth
chat), 100 KB tool outputs, and a 200 MB project of 5,000 files in the first
folder.

Writes project folders, Wada settings/workspace databases, and PiG transcripts
with their Wada metadata. Run with WADA_DATA_DIR=ROOT/data. The embedded SDK
reads these conversations without an agent executable or a model request.
"""

import base64
import json
import os
import random
import sqlite3
import struct
import sys
import time
import uuid
import zlib

args = sys.argv[1:]
heavy = "--heavy" in args
args = [a for a in args if a != "--heavy"]
if len(args) != 1:
    sys.exit("usage: seed.py [--heavy] ROOT")
root = os.path.abspath(args[0])
projects = os.path.join(root, "projects")
data = os.path.join(root, "data")
pig = os.path.join(data, "pig")
sessions = os.path.join(pig, "sessions")
metadata = os.path.join(pig, "metadata")
for d in (projects, sessions, metadata, data):
    os.makedirs(d, exist_ok=True)

rng = random.Random(7)
now_ms = int(time.time() * 1000)

# Twelve folders with a falling number of chats; one long name.
FOLDERS = [
    ("wada_desktop_go", 14), ("onevra", 9), ("billing-service", 7), ("mobile-app", 6),
    ("infra", 5), ("design-system", 4), ("data-pipeline", 3), ("docs-site", 3),
    ("a-folder-with-a-really-quite-long-name", 2), ("scratch", 2), ("cli", 1), ("empty-project", 0),
]

TOPICS = [
    "Fix the flaky parser test", "Add a --verbose flag", "Why does the build fail on CI?",
    "Refactor the settings store", "Explain the kernel scopes", "Speed up the transcript",
    "Write tests for the workspace record", "Rename the sessions plugin", "Review the tab strip",
    "Port the sidebar search to All chats", "Add dark mode tokens", "Investigate the memory spike",
    "Draft release notes for 0.3", "Update the agent integration", "Clean up unused helpers",
    "Make the composer keep its focus", "Add a retry button to failed steps",
    "Find where the tooltip flickers", "Document the agent contract", "Bump dependencies",
]

PARAGRAPHS = [
    "I looked through the code paths involved. The problem comes from the order in which the "
    "listeners run: the second one reads state the first has not written yet.",
    "Here is what I changed:\n\n- moved the read after the write\n- added a test that fails "
    "without the fix\n- kept the public API as it was",
    "The cause is a stale cache. Each frame rebuilds the list, but the cache key ignores the "
    "query, so a new search shows the old rows until something else invalidates it.",
    "```go\nfunc (p *Plugin) layout() []item {\n\tif p.stale || p.query != p.lastQuery {\n"
    "\t\tp.rows, p.lastQuery, p.stale = p.items(), p.query, false\n\t}\n\treturn p.rows\n}\n```",
    "| Case | Before | After |\n|---|---:|---:|\n| empty | 236 µs | 144 µs |\n| long | 862 µs | 398 µs |",
    "That should be all. Run `make check` to confirm; it passes here. Let me know if you want the "
    "same change in the other plugins too.",
    "> Note: the backend never retries a mutation, so a failed rename is reported and the user "
    "decides what to do next.",
    "## Summary\n\nThe fix is small, but the test around it is the important part: it pins the "
    "ordering so this can't come back quietly.",
]

USER_LINES = [
    "can you look at this?", "ok, go ahead", "what about the dark theme?", "run the tests again",
    "that looks wrong, the count is off by one", "great, now the docs", "why is it slow?",
    "show me the diff", "keep the old behaviour for drafts", "commit it",
]


hrng = random.Random(11)  # --heavy draws from its own generator, so the plain seed stays as it was

WORDS = ("func return if err != nil { } p.rows ctx kernel.Go state := range for _, item "
         "ui.Column(c).Children(func() string int64 append(out, len( strings.TrimSpace defer "
         "// a comment on the next line, which explains why").split()


def code_text(size):
    """Source-like text of about size bytes."""
    out, n = [], 0
    while n < size:
        line = "\t" * hrng.randint(0, 3) + " ".join(hrng.choice(WORDS) for _ in range(hrng.randint(3, 12)))
        out.append(line)
        n += len(line) + 1
    return "\n".join(out)[:size]


def png(width, height):
    """An RGB PNG that looks like a window: a title bar, a sidebar and text lines."""
    def chunk(kind, data):
        return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data) & 0xffffffff)
    bar = b"\xe8\xe8\xec" * width
    side = int(width * .2)
    plain = b"\xf4\xf4\xf6" * side + b"\xff\xff\xff" * (width - side)
    lines = [b"\xf4\xf4\xf6" * side + b"\xff\xff\xff" * 40 + b"\x30\x30\x38" * w + b"\xff\xff\xff" * (width - side - 40 - w)
             for w in (int((width - side - 80) * f) for f in (.9, .7, .82, .4, .66))]
    rows = []
    for y in range(height):
        if y < 56:
            row = bar
        elif y % 36 < 14:
            row = lines[(y // 36) % len(lines)]
        else:
            row = plain
        rows.append(b"\x00" + row)
    data = zlib.compress(b"".join(rows), 6)
    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0)) +
            chunk(b"IDAT", data) + chunk(b"IEND", b""))


def b64(data):
    return base64.b64encode(data).decode()


def entry_id():
    return uuid.uuid4().hex[:8]


def iso(ms):
    return time.strftime("%Y-%m-%dT%H:%M:%S.000Z", time.gmtime(ms / 1000))


def write_session(cwd, title, turns, updated_ms, image=None):
    sid = str(uuid.uuid4())
    lines = [{"type": "session", "version": 3, "id": sid, "timestamp": iso(updated_ms - turns * 60000), "cwd": cwd}]
    parent = None

    def add(e):
        nonlocal parent
        e["id"] = entry_id()
        e["parentId"] = parent
        e.setdefault("timestamp", iso(updated_ms))
        lines.append(e)
        parent = e["id"]

    add({"type": "model_change", "provider": "anthropic", "modelId": "claude-sonnet-5-5"})
    add({"type": "session_info", "name": title})
    tokens = 4000
    for t in range(turns):
        text = title if t == 0 else rng.choice(USER_LINES)
        content = [{"type": "text", "text": text}]
        if t == 0 and image:
            content.append({"type": "image", "data": b64(image), "mimeType": "image/png"})
        add({"type": "message", "message": {"role": "user", "content": content}})
        blocks = []
        if rng.random() < 0.3:
            blocks.append({"type": "thinking", "thinking": "Let me check the relevant files first."})
        tool_results = []
        for _ in range(rng.choice([0, 0, 1, 2, 3])):
            call = "call_" + entry_id()
            kind = rng.choice(["bash", "read", "edit", "grep"])
            args = {"command": rng.choice(["go test ./...", "make check", "git status", "rg Sidebar internal/"])} \
                if kind == "bash" else {"path": rng.choice(["internal/plugins/tabs/strip.go", "main.go", "docs/design/shell-and-plugins.md"])}
            blocks.append({"type": "toolCall", "id": call, "name": kind, "arguments": args})
            out = "ok  \twada-desktop-go/internal/plugins/tabs\t0.41s\nPASS" if kind == "bash" else "(42 lines)"
            if heavy and kind == "read" and hrng.random() < .5:
                out = code_text(100 << 10)
            tool_results.append({"role": "toolResult", "toolCallId": call, "content": [{"type": "text", "text": out}],
                                 "isError": False})
        body = "\n\n".join(rng.sample(PARAGRAPHS, rng.randint(1, 3)))
        blocks.append({"type": "text", "text": body})
        tokens += rng.randint(800, 6000)
        usage = {"input": rng.randint(200, 2000), "output": rng.randint(200, 1500), "reasoning": 0,
                 "cacheRead": tokens, "cacheWrite": rng.randint(0, 800),
                 "cost": {"total": round(rng.uniform(0.002, 0.08), 4)}}
        add({"type": "message", "message": {"role": "assistant", "content": blocks, "provider": "anthropic",
                                            "model": "claude-sonnet-5-5", "usage": usage}})
        for r in tool_results:
            add({"type": "message", "message": r})
    path = os.path.join(sessions, f"{sid}.jsonl")
    with open(path, "w") as f:
        for line in lines:
            f.write(json.dumps(line) + "\n")
    os.utime(path, (updated_ms / 1000, updated_ms / 1000))
    attachments, baselines = {}, {}
    if image:
        attachments["0"] = {"Text": title, "Files": [{"Name": "screenshot.png", "MIME": "image/png", "Data": b64(image)}]}
    if heavy:
        for k in range(12):
            baselines[f"internal/pkg{k}/file{k}.go"] = {"Exists": True, "Data": b64(code_text(64 << 10).encode())}
    with open(os.path.join(metadata, sid + ".json"), "w") as f:
        json.dump({"Session": {"ID": sid, "Title": title, "Directory": cwd,
                   "Updated": updated_ms, "Model": {"Provider": "anthropic", "ID": "claude-sonnet-5-5"}},
                   "Path": path, "HasMessages": True, "Mode": "ask", "Attachments": attachments,
                   "Baselines": baselines}, f)

    return sid


small_image = png(1600, 1000) if heavy else None
folders, convs = [], []
for i, (name, chats) in enumerate(FOLDERS):
    path = os.path.join(projects, name)
    os.makedirs(path, exist_ok=True)
    folders.append((path, now_ms - (40 - i) * 86400000))
    for j in range(chats):
        # Mostly short chats, some long ones; the first chats are the newest.
        turns = rng.choice([1, 2, 3, 4, 6, 8, 12]) if rng.random() > 0.12 else rng.choice([60, 120, 200])
        updated = now_ms - (i * 7 + j) * 3600000 - rng.randint(0, 3000000)
        title = rng.choice(TOPICS) + ("" if j < len(TOPICS) else f" ({j})")
        image = small_image if heavy and len(convs) % 4 == 3 else None
        sid = write_session(path, title, turns, updated, image)
        convs.append((sid, path, title, updated - turns * 60000, updated))

if heavy:
    # A large project in the first folder: 5,000 files of about 40 KB.
    corpus = code_text(96 << 10)
    for d in range(50):
        sub = os.path.join(folders[0][0], "src", f"pkg{d:02}")
        os.makedirs(sub, exist_ok=True)
        for k in range(100):
            at = hrng.randint(0, len(corpus) - (40 << 10))
            with open(os.path.join(sub, f"file{k:03}.go"), "w") as f:
                f.write(f"// pkg{d:02}/file{k:03}\n" + corpus[at:at + (40 << 10)])
    # The longest chat starts with a full-size Retina screenshot.
    sid = max(convs, key=lambda c: os.path.getsize(os.path.join(sessions, c[0] + ".jsonl")))[0]
    shot = png(5120, 2880)
    path = os.path.join(sessions, sid + ".jsonl")
    with open(path) as f:
        lines = [json.loads(line) for line in f]
    first = next(e for e in lines if e["type"] == "message" and e["message"]["role"] == "user")
    first["message"]["content"].append({"type": "image", "data": b64(shot), "mimeType": "image/png"})
    stat = os.stat(path)
    with open(path, "w") as f:
        for line in lines:
            f.write(json.dumps(line) + "\n")
    os.utime(path, (stat.st_atime, stat.st_mtime))
    meta = os.path.join(metadata, sid + ".json")
    with open(meta) as f:
        record = json.load(f)
    record["Attachments"]["0"] = {"Text": first["message"]["content"][0]["text"],
                                  "Files": [{"Name": "screenshot.png", "MIME": "image/png", "Data": b64(shot)}]}
    with open(meta, "w") as f:
        json.dump(record, f)

origin = "pig " + pig

ws = sqlite3.connect(os.path.join(data, "workspace.db"))
ws.executescript("""
CREATE TABLE folders (path TEXT PRIMARY KEY, collapsed INTEGER NOT NULL, added INTEGER NOT NULL);
CREATE TABLE conversations (origin TEXT NOT NULL, session TEXT NOT NULL, folder TEXT NOT NULL, title TEXT NOT NULL,
  model_provider TEXT NOT NULL, model_id TEXT NOT NULL, model_variant TEXT NOT NULL,
  created INTEGER NOT NULL, updated INTEGER NOT NULL, PRIMARY KEY (origin, session));
PRAGMA user_version = 1;
""")
ws.executemany("INSERT INTO folders VALUES (?, 0, ?)", folders)
ws.executemany("INSERT INTO conversations VALUES (?, ?, ?, ?, 'anthropic', 'claude-sonnet-5-5', '', ?, ?)",
               [(origin, sid, folder, title, created, updated) for sid, folder, title, created, updated in convs])
ws.commit()

# Open tabs: the newest chats of the first eight folders, 24 in all, grouped as the strip groups them.
open_tabs = []
for path, _ in folders[:8]:
    mine = sorted((c for c in convs if c[1] == path), key=lambda c: -c[4])[:4]
    open_tabs += [{"kind": 0, "session": c[0], "folder": path, "title": c[2]} for c in mine]
open_tabs = open_tabs[:24]

st = sqlite3.connect(os.path.join(data, "settings.db"))
st.executescript("""
CREATE TABLE settings (namespace TEXT NOT NULL, key TEXT NOT NULL, value TEXT NOT NULL, PRIMARY KEY (namespace, key));
PRAGMA user_version = 1;
""")
st.executemany("INSERT INTO settings VALUES (?, ?, ?)", [
    ("tabs", "open", json.dumps(open_tabs)),
    ("tabs", "active", "0"),
])
st.commit()

longest = max(convs, key=lambda c: os.path.getsize(os.path.join(sessions, c[0] + ".jsonl")))
print(f"{len(folders)} folders, {len(convs)} chats, {len(open_tabs)} open tabs; longest chat: {longest[2]!r}")
print(f"WADA_DATA_DIR={data}")
