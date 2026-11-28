import { useEffect, useState } from "react";
import type { ProjectInfo, TerminalInfo, WorkspaceConfig, WorkspaceInfo } from "./client/protocol";
import type { WadaClient } from "./client/wadaClient";
import { workspaceName } from "./client/workspaces";

/**
 * WorkspacePanel shows one workspace: its branch and base, its wada.json
 * (trusting it, running its setup, starting and stopping its services) and
 * Archive. Terminals it starts open in the Terminal tab through onTerminal.
 */
export function WorkspacePanel({
  cli,
  workspace,
  project,
  terminals,
  host,
  onTerminal,
  onError,
}: {
  cli: WadaClient;
  workspace: WorkspaceInfo;
  project?: ProjectInfo;
  terminals: TerminalInfo[];
  host: string; // where the daemon's services are reached, for port links
  onTerminal: (id: string) => void;
  onError: (msg: string) => void;
}) {
  const [config, setConfig] = useState<WorkspaceConfig | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState("");
  const [confirmForce, setConfirmForce] = useState("");

  const id = workspace.id;
  async function reload(): Promise<void> {
    try {
      const c = await cli.workspaceConfig(id);
      setConfig(c);
    } catch (err) {
      setConfig(null);
      onError(text(err));
    } finally {
      setLoading(false);
    }
  }
  useEffect(() => {
    setLoading(true);
    setConfig(null);
    setConfirmForce("");
    void reload();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cli, id]);

  // A run's terminal is live in the terminal list; the config's copy is when it was read.
  const running = (t?: TerminalInfo) => !!t && (terminals.find((x) => x.id === t.id)?.running ?? t.running);

  async function act(what: string, fn: () => Promise<unknown>): Promise<void> {
    setBusy(what);
    try {
      await fn();
      await reload();
    } catch (err) {
      onError(`${what}: ${text(err)}`);
    } finally {
      setBusy("");
    }
  }

  async function archive(force: boolean): Promise<void> {
    setBusy("Archive");
    try {
      await cli.archiveWorkspace(id, force);
      setConfirmForce("");
    } catch (err) {
      const msg = text(err);
      if (!force && msg.startsWith("conflict")) {
        // Unsaved work, or a teardown that failed: the user decides.
        setConfirmForce(msg.replace(/^conflict:\s*/, ""));
      } else {
        // An archive is not retried: its outcome is unknown.
        onError(`Archiving may not have finished: ${msg}`);
      }
    } finally {
      setBusy("");
    }
  }

  const worktree = workspace.isolation !== "project";
  return (
    <div className="panel workspace-panel">
      <section className="ws-head">
        <div>
          <h3>{workspaceName(workspace, project)}</h3>
          <div className="muted">{workspace.directory}</div>
          <div className="row">
            {workspace.branch && <span className="pill">{workspace.branch}</span>}
            {workspace.baseRef && <span className="pill">from {workspace.baseRef}</span>}
            {workspace.pr ? <span className="pill">PR #{workspace.pr}</span> : null}
          </div>
        </div>
        {worktree && (
          <button className="btn btn-danger" disabled={busy !== ""} onClick={() => void archive(false)}>
            Archive
          </button>
        )}
      </section>

      {confirmForce && (
        <div className="approval-card">
          <div>
            <strong>Archive anyway?</strong> {confirmForce}
          </div>
          <div className="muted">Archiving anyway discards what isn't committed and pushed.</div>
          <div className="row">
            <button className="btn btn-danger" disabled={busy !== ""} onClick={() => void archive(true)}>
              Discard and archive
            </button>
            <button className="btn" onClick={() => setConfirmForce("")}>
              Keep it
            </button>
          </div>
        </div>
      )}

      <section>
        <div className="side-title">wada.json</div>
        {loading && <p className="muted">Reading…</p>}
        {!loading && config && !config.found && (
          <p className="muted">No wada.json here. Add one with setup, services and teardown to run them from Wada.</p>
        )}
        {config?.error && <div className="banner-error">{config.error}</div>}
        {config?.found && !config.error && !config.trusted && (
          <div className="approval-card">
            <div>
              <strong>Trust this wada.json?</strong> It runs these commands in {project?.name || "this project"}:
            </div>
            <pre className="tool-out">
              {[
                ...(config.setup || []).map((c) => "setup: " + c),
                ...(config.services || []).map((s) => `${s.name}: ${s.command}`),
                ...(config.teardown || []).map((c) => "teardown: " + c),
              ].join("\n")}
            </pre>
            <div className="row">
              <button
                className="btn btn-primary"
                disabled={busy !== "" || !config.hash}
                onClick={() => void act("Trust", () => cli.trustWorkspaceConfig(id, config.hash || ""))}
              >
                Trust and allow
              </button>
            </div>
          </div>
        )}
        {config?.found && config.trusted && (
          <div className="ws-runs">
            {(config.setup?.length || 0) > 0 && (
              <div className="ws-run">
                <div>
                  <strong>Setup</strong>
                  <div className="muted mono">{config.setup?.join(" && ")}</div>
                </div>
                <div className="row">
                  {config.setupTerminal && (
                    <button className="btn btn-small" onClick={() => onTerminal(config.setupTerminal!.id)}>
                      {running(config.setupTerminal) ? "Running…" : "Output"}
                    </button>
                  )}
                  <button
                    className="btn btn-small"
                    disabled={busy !== "" || running(config.setupTerminal)}
                    onClick={() =>
                      void act("Setup", async () => {
                        const t = await cli.runSetup(id);
                        onTerminal(t.id);
                      })
                    }
                  >
                    Run setup
                  </button>
                </div>
              </div>
            )}
            {(config.services || []).map((s) => {
              const up = running(s.terminal);
              return (
                <div key={s.name} className="ws-run">
                  <div>
                    <strong>{s.name}</strong>{" "}
                    <span className={up ? "pill pill-green" : "pill"}>{up ? "running" : "stopped"}</span>
                    <div className="muted mono">{s.command}</div>
                    {up && host && (
                      <a href={`http://${host}:${s.port}`} target="_blank" rel="noreferrer">
                        {host}:{s.port}
                      </a>
                    )}
                    {!up && <div className="muted">port {s.port}</div>}
                  </div>
                  <div className="row">
                    {s.terminal && (
                      <button className="btn btn-small" onClick={() => onTerminal(s.terminal!.id)}>
                        Output
                      </button>
                    )}
                    {up ? (
                      <button
                        className="btn btn-small btn-danger"
                        disabled={busy !== ""}
                        onClick={() => void act(`Stop ${s.name}`, () => cli.stopService(id, s.name))}
                      >
                        Stop
                      </button>
                    ) : (
                      <button
                        className="btn btn-small"
                        disabled={busy !== ""}
                        onClick={() =>
                          void act(`Start ${s.name}`, async () => {
                            const t = await cli.startService(id, s.name);
                            onTerminal(t.id);
                          })
                        }
                      >
                        Start
                      </button>
                    )}
                  </div>
                </div>
              );
            })}
            {!config.setup?.length && !config.services?.length && (
              <p className="muted">This wada.json has no setup or services.</p>
            )}
          </div>
        )}
      </section>
    </div>
  );
}

function text(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
