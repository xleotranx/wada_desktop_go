import { useState } from "react";
import type { Schedule, ScheduleSpec } from "./client/protocol";
import type { WadaClient } from "./client/wadaClient";

/**
 * SchedulesPanel lists the daemon's schedules with pause, run and delete,
 * and adds one: a heartbeat for the open conversation, or a schedule that
 * starts a conversation in the new chat's folder or workspace each run.
 */
export function SchedulesPanel({
  cli,
  schedules,
  session,
  sessionTitle,
  directory,
  workspace,
  onChanged,
  onError,
}: {
  cli: WadaClient;
  schedules: Schedule[];
  session: string; // the open conversation, for a heartbeat
  sessionTitle: string;
  directory: string; // where a new schedule's conversations start
  workspace: string;
  onChanged: (sc: Schedule) => void;
  onError: (msg: string) => void;
}) {
  const [adding, setAdding] = useState<"" | "heartbeat" | "schedule">("");
  const [busy, setBusy] = useState("");

  async function act(sc: Schedule, what: string, fn: () => Promise<Schedule | void>): Promise<void> {
    setBusy(sc.id);
    try {
      const out = await fn();
      if (out) onChanged(out);
    } catch (err) {
      onError(`${what}: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setBusy("");
    }
  }

  const beats = schedules.filter((s) => s.session && s.session === session);
  return (
    <div className="panel workspace-panel">
      <section className="ws-head">
        <div>
          <h3>Schedules</h3>
          <div className="muted">Run by the daemon in its local time. A run that fails is not retried.</div>
        </div>
        <div className="row">
          {session && (
            <button className="btn" onClick={() => setAdding("heartbeat")}>
              + Heartbeat
            </button>
          )}
          <button className="btn btn-primary" onClick={() => setAdding("schedule")}>
            + Schedule
          </button>
        </div>
      </section>

      {adding && (
        <ScheduleForm
          heartbeat={adding === "heartbeat"}
          target={
            adding === "heartbeat"
              ? `Sends to “${sessionTitle || "this conversation"}”; a beat while it's working is skipped.`
              : `Starts a conversation in ${directory || "the inbox"} each run.`
          }
          onCancel={() => setAdding("")}
          onSubmit={async (spec) => {
            if (adding === "heartbeat") spec.session = session;
            else if (workspace) spec.workspace = workspace;
            else if (directory) spec.directory = directory;
            const sc = await cli.createSchedule(spec);
            onChanged(sc);
            setAdding("");
          }}
        />
      )}

      {session && beats.length === 0 && !adding && (
        <p className="muted">This conversation has no heartbeat.</p>
      )}
      {schedules.length === 0 && <p className="muted">No schedules yet.</p>}
      <div className="ws-runs">
        {schedules.map((sc) => (
          <div key={sc.id} className="ws-run">
            <div>
              <strong>{sc.session ? "♥ " : ""}{sc.name}</strong>{" "}
              <span className="pill">{sc.cron ? `cron ${sc.cron}` : `every ${sc.every}`}</span>{" "}
              <span className="pill">{sc.paused || !sc.nextRun ? "paused" : "next " + stamp(sc.nextRun)}</span>
              <div className="muted mono">{sc.prompt}</div>
              {sc.lastRun ? (
                <div className={sc.lastStatus === "failed" ? "error-text" : "muted"}>
                  {sc.lastStatus} {stamp(sc.lastRun)}
                  {sc.lastError ? `: ${sc.lastError}` : ""}
                </div>
              ) : null}
            </div>
            <div className="row">
              <button
                className="btn btn-small"
                disabled={busy === sc.id}
                onClick={() => void act(sc, "Run", () => cli.runSchedule(sc.id))}
              >
                Run now
              </button>
              <button
                className="btn btn-small"
                disabled={busy === sc.id}
                onClick={() =>
                  void act(sc, sc.paused ? "Resume" : "Pause", () =>
                    cli.updateSchedule(sc.id, { ...specOf(sc), paused: !sc.paused })
                  )
                }
              >
                {sc.paused ? "Resume" : "Pause"}
              </button>
              <button
                className="btn btn-small btn-danger"
                disabled={busy === sc.id}
                onClick={() =>
                  void act(sc, "Delete", async () => {
                    await cli.deleteSchedule(sc.id);
                    return { ...sc, removed: true };
                  })
                }
              >
                Delete
              </button>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

function ScheduleForm({
  heartbeat,
  target,
  onSubmit,
  onCancel,
}: {
  heartbeat: boolean;
  target: string;
  onSubmit: (spec: ScheduleSpec) => Promise<void>;
  onCancel: () => void;
}) {
  const [name, setName] = useState("");
  const [prompt, setPrompt] = useState(heartbeat ? "Check how this is going and continue where it stopped." : "");
  const [kind, setKind] = useState<"every" | "cron">(heartbeat ? "every" : "cron");
  const [when, setWhen] = useState(heartbeat ? "30m" : "0 9 * * 1-5");
  const [mode, setMode] = useState("ask");
  const [profile, setProfile] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function submit(e: React.FormEvent): Promise<void> {
    e.preventDefault();
    const spec: ScheduleSpec = { name: name.trim(), prompt };
    if (kind === "cron") spec.cron = when.trim();
    else spec.every = when.trim();
    if (!heartbeat) {
      spec.mode = mode;
      if (profile.trim()) spec.profile = profile.trim();
    }
    setBusy(true);
    setError("");
    try {
      await onSubmit(spec);
    } catch (err) {
      setError(`The schedule may not have been saved: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="approval-card" onSubmit={(e) => void submit(e)}>
      <strong>{heartbeat ? "New heartbeat" : "New schedule"}</strong>
      <div className="muted">{target}</div>
      <input value={name} onChange={(e) => setName(e.target.value)} placeholder="Name (default: the prompt's first line)" />
      <textarea rows={3} value={prompt} onChange={(e) => setPrompt(e.target.value)} placeholder="Prompt" required />
      <div className="row">
        <select value={kind} onChange={(e) => setKind(e.target.value as "every" | "cron")}>
          <option value="every">Every</option>
          <option value="cron">Cron</option>
        </select>
        <input
          value={when}
          onChange={(e) => setWhen(e.target.value)}
          placeholder={kind === "cron" ? "minute hour day month weekday" : "30m, 2h"}
          required
        />
        {!heartbeat && (
          <>
            <select value={mode} onChange={(e) => setMode(e.target.value)}>
              <option value="ask">Ask</option>
              <option value="full">Without approvals</option>
            </select>
            <input value={profile} onChange={(e) => setProfile(e.target.value)} placeholder="Profile (optional)" />
          </>
        )}
      </div>
      {error && <div className="banner-error">{error}</div>}
      <div className="row end">
        <button type="button" className="btn" onClick={onCancel}>
          Cancel
        </button>
        <button type="submit" className="btn btn-primary" disabled={busy || !prompt.trim() || !when.trim()}>
          {busy ? "Adding…" : "Add"}
        </button>
      </div>
    </form>
  );
}

function specOf(sc: Schedule): ScheduleSpec {
  const { name, prompt, cron, every, session, directory, workspace, provider, profile, mode, paused } = sc;
  return { name, prompt, cron, every, session, directory, workspace, provider, profile, mode, paused };
}

function stamp(ms: number): string {
  const t = new Date(ms);
  const now = new Date();
  const time = t.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  return t.toDateString() === now.toDateString() ? time : `${t.toLocaleDateString([], { month: "short", day: "numeric" })} ${time}`;
}
