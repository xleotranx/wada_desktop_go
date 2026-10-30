"""Measure the real window under a replayed workload: CPU, wakeups, frames and
physical footprint, before, during and after the replay.

Usage: python3 -I scripts/measure.py [--heavy] [--keep] [--runs N] MODE

MODE is text, working or background (see internal/plugins/replay). The script
seeds a temporary data folder (with --heavy, the heavy seed), builds the app
with the wadareplay and mygo_noinspector tags, runs it with
MYGO_FRAME_STATS=all and reads the process's own resource accounting
(proc_pid_rusage), which neither samples nor pauses it: CPU time, idle
wakeups, the current footprint and the highest footprint of each phase.
--keep leaves the temporary folder; --runs repeats the run and prints each.

Phases: startup (launch until the replay starts, WADA_REPLAY_DELAY), replay
(WADA_REPLAY_FOR) and settled (the WADA_REPLAY_SETTLE after it). CPU is a
percentage of one core.
"""

import ctypes
import os
import re
import shutil
import statistics
import subprocess
import sys
import tempfile
import threading
import time

args = sys.argv[1:]
heavy, keep = "--heavy" in args, "--keep" in args
runs = 1
if "--runs" in args:
    runs = int(args[args.index("--runs") + 1])
    del args[args.index("--runs"):args.index("--runs") + 2]
args = [a for a in args if a not in ("--heavy", "--keep")]
if len(args) != 1 or args[0] not in ("text", "working", "background"):
    sys.exit(__doc__)
mode = args[0]
repo = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

libc = ctypes.CDLL("/usr/lib/libSystem.B.dylib", use_errno=True)


class Timebase(ctypes.Structure):
    _fields_ = [("numer", ctypes.c_uint32), ("denom", ctypes.c_uint32)]


tb = Timebase()
libc.mach_timebase_info(ctypes.byref(tb))
RUSAGE_INFO_V4 = 4
# rusage_info_v4 after its 16-byte UUID, as uint64s (sys/resource.h).
USER, SYSTEM, IDLE_WAKEUPS, FOOTPRINT, LIFETIME_MAX, INTERVAL_MAX = 0, 1, 2, 7, 28, 33


def rusage(pid):
    buf = (ctypes.c_uint64 * 40)()
    if libc.proc_pid_rusage(pid, RUSAGE_INFO_V4, ctypes.byref(buf)) != 0:
        return None
    v = buf[2:]  # skip the UUID
    cpu = (v[USER] + v[SYSTEM]) * tb.numer / tb.denom / 1e9
    return {"cpu": cpu, "wakeups": v[IDLE_WAKEUPS], "footprint": v[FOOTPRINT],
            "lifetime_max": v[LIFETIME_MAX], "interval_max": v[INTERVAL_MAX], "t": time.monotonic()}


def reset_interval(pid):
    try:
        libc.proc_reset_footprint_interval(pid)
        return True
    except AttributeError:
        return False


FRAME = re.compile(r"mygo: frame \d+ took ([\d.]+) ms: build ([\d.]+)( \(painted again\)| \(\d+ passes\))?, "
                   r"layout ([\d.]+), paint ([\d.]+), present ([\d.]+)")


def mib(b):
    return f"{b / 1048576:.1f}"


def pct(xs, p):
    xs = sorted(xs)
    return xs[min(len(xs) - 1, int(round(p / 100 * (len(xs) - 1))))] if xs else 0


def run_once(binary, data):
    env = dict(os.environ, WADA_DATA_DIR=data, WADA_REPLAY=mode, MYGO_FRAME_STATS="all")
    proc = subprocess.Popen([binary], env=env, stderr=subprocess.PIPE, stdout=subprocess.DEVNULL, text=True)
    marks, frames, phase = {"launch": rusage(proc.pid)}, {"startup": [], "replay": [], "settled": []}, ["startup"]
    peaks = {}
    footprints = {"startup": [], "replay": [], "settled": []}
    lock = threading.Lock()

    def read():
        for line in proc.stderr:
            m = FRAME.search(line)
            with lock:
                if m:
                    total, build, kind, layout, paint, present = m.groups()
                    frames[phase[0]].append((float(total), float(build), float(layout), float(paint), float(present),
                                             kind == " (painted again)"))
                    continue
                if "replay: start" in line or "replay: end" in line or "replay: quit" in line:
                    r = rusage(proc.pid)
                    name = {"start": "startup", "end": "replay", "quit": "settled"}[line.split()[1]]
                    if r:
                        marks[name] = r
                        peaks[name] = r["interval_max"]
                        reset_interval(proc.pid)
                    phase[0] = {"startup": "replay", "replay": "settled", "settled": "settled"}[name]
                    if name == "startup" and "window in front: false" in line:
                        sys.stderr.write("note: the window was not in front during the replay\n")
                elif "replay:" in line or "panic" in line or "error" in line.lower():
                    sys.stderr.write(line)

    reader = threading.Thread(target=read, daemon=True)
    reader.start()
    while proc.poll() is None:
        r = rusage(proc.pid)
        if r:
            with lock:
                footprints[phase[0]].append(r["footprint"])
        time.sleep(.25)
    reader.join(5)
    if "settled" not in marks:
        sys.exit(f"the app exited ({proc.returncode}) before the replay finished")
    rows = []
    prev = marks["launch"]
    for name in ("startup", "replay", "settled"):
        m = marks[name]
        wall = m["t"] - prev["t"]
        fs = frames[name]
        built = [f for f in fs if not f[5]]
        rows.append({
            "phase": name, "seconds": wall, "cpu": 100 * (m["cpu"] - prev["cpu"]) / wall,
            "wakeups/s": (m["wakeups"] - prev["wakeups"]) / wall,
            "frames/s": len(fs) / wall, "built/s": len(built) / wall,
            "p50": pct([f[0] for f in fs], 50), "p95": pct([f[0] for f in fs], 95),
            "max": max([f[0] for f in fs], default=0),
            "build": statistics.median([f[1] for f in built]) if built else 0,
            "layout": statistics.median([f[2] for f in built]) if built else 0,
            "paint": statistics.median([f[3] for f in fs]) if fs else 0,
            "present": statistics.median([f[4] for f in fs]) if fs else 0,
            "peak": peaks.get(name, 0), "end": m["footprint"],
        })
        prev = m
    return rows


def main():
    tmp = tempfile.mkdtemp(prefix="wada-measure-")
    try:
        binary = os.path.join(tmp, "wada_replay")
        subprocess.run(["go", "build", "-tags", "wadareplay mygo_noinspector", "-trimpath", "-ldflags", "-s -w",
                        "-o", binary, "."], cwd=repo, check=True)
        print(f"mode {mode}{' (heavy seed)' if heavy else ''}")
        print(f"{'phase':<9}{'sec':>5}{'CPU%':>7}{'wake/s':>8}{'frm/s':>7}{'built/s':>8}{'p50':>6}{'p95':>6}{'max':>6}"
              f"{'build':>7}{'layout':>7}{'paint':>6}{'pres':>6}{'peakMiB':>9}{'endMiB':>8}")
        for n in range(runs):
            # Every run starts from a fresh seed, whose records hold absolute paths.
            root = os.path.join(tmp, f"run{n}")
            seed = [sys.executable, "-I", os.path.join(repo, "scripts", "seed.py")] + (["--heavy"] if heavy else [])
            subprocess.run(seed + [root], check=True, stdout=subprocess.DEVNULL)
            for r in run_once(binary, os.path.join(root, "data")):
                print(f"{r['phase']:<9}{r['seconds']:>5.0f}{r['cpu']:>7.1f}{r['wakeups/s']:>8.0f}{r['frames/s']:>7.1f}"
                      f"{r['built/s']:>8.1f}{r['p50']:>6.1f}{r['p95']:>6.1f}{r['max']:>6.1f}{r['build']:>7.2f}"
                      f"{r['layout']:>7.2f}{r['paint']:>6.2f}{r['present']:>6.2f}{mib(r['peak']):>9}{mib(r['end']):>8}")
            shutil.rmtree(root, ignore_errors=True)
    finally:
        if keep:
            print("kept", tmp)
        else:
            shutil.rmtree(tmp, ignore_errors=True)


main()
