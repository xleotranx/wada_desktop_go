import { useEffect, useRef, useState } from "react";
import { formatTerminalOutput } from "./client/ansi";
import type {
  ApprovalItem,
  DaemonEvent,
  FileChange,
  HarnessInfo,
  SessionInfo,
  SessionMessage,
  TerminalInfo,
  WorkerInfo,
} from "./client/protocol";
import {
  type SavedHost,
  deleteHost,
  loadActiveHostId,
  loadSavedHosts,
  saveActiveHostId,
  upsertHost,
} from "./client/storage";
import { type AttachedTerminal, WadaClient } from "./client/wadaClient";
import { decodeOffer } from "./crypto/offer";

type RightTab = "chat" | "diffs" | "terminal";

export function App() {
  const clientRef = useRef<WadaClient | null>(null);
  const termAttachRef = useRef<AttachedTerminal | null>(null);

  const [hosts, setHosts] = useState<SavedHost[]>(() => loadSavedHosts());
  const [activeHostId, setActiveHostId] = useState<string>(() => loadActiveHostId());
  const [connState, setConnState] = useState<"disconnected" | "connecting" | "connected">(
    "disconnected"
  );
  const [connError, setConnError] = useState("");
  const [e2eeMode, setE2eeMode] = useState<"relay" | "direct" | "">("");

  const [pairOpen, setPairOpen] = useState(false);
  const [pairName, setPairName] = useState("My Workstation");
  const [pairInput, setPairInput] = useState("");
  const [pairPassword, setPairPassword] = useState("");
  const [sidebarOpen, setSidebarOpen] = useState(false);

  const [sessions, setSessions] = useState<SessionInfo[]>([]);
  const [harnesses, setHarnesses] = useState<HarnessInfo[]>([]);
  const [selectedSessionId, setSelectedSessionId] = useState("");
  const [messages, setMessages] = useState<SessionMessage[]>([]);
  const [liveDelta, setLiveDelta] = useState("");
  const [approvals, setApprovals] = useState<ApprovalItem[]>([]);
  const [workers, setWorkers] = useState<WorkerInfo[]>([]);
  const [changes, setChanges] = useState<FileChange[]>([]);
  const [rightTab, setRightTab] = useState<RightTab>("chat");

  const [promptText, setPromptText] = useState("");
  const [newDir, setNewDir] = useState(".");
  const [newProvider, setNewProvider] = useState("");
  const [mode, setMode] = useState<"ask" | "full">("ask");
  const [busyTurn, setBusyTurn] = useState(false);

  const [terminals, setTerminals] = useState<TerminalInfo[]>([]);
  const [activeTermId, setActiveTermId] = useState("");
  const [termOutput, setTermOutput] = useState("");
  const [termCmd, setTermCmd] = useState("");

  // Extract #offer= from initial URL fragment and wipe it from browser history.
  useEffect(() => {
    const hash = window.location.hash;
    if (hash.includes("#offer=") || hash.startsWith("#offer=")) {
      try {
        const offer = decodeOffer(hash);
        window.history.replaceState(null, "", window.location.pathname + window.location.search);
        const host: SavedHost = {
          id: offer.serverId || crypto.randomUUID(),
          name: offer.hostname || offer.serverId || "Paired Host",
          mode: offer.relayUrl ? "relay" : "direct",
          offer: hash,
          relayUrl: offer.relayUrl,
          directUrl: offer.directUrl,
          serverId: offer.serverId,
          token: offer.token,
          requiresPassword: offer.requiresPassword,
          addedAt: Date.now(),
        };
        const next = upsertHost(host);
        setHosts(next);
        setActiveHostId(host.id);
        saveActiveHostId(host.id);
        if (offer.requiresPassword) {
          setPairName(host.name);
          setPairInput(hash);
          setPairOpen(true);
        } else {
          void connectHost(host);
        }
      } catch (err) {
        setConnError(err instanceof Error ? err.message : "Invalid pairing link");
      }
    } else if (hosts.length > 0) {
      const target = hosts.find((h) => h.id === activeHostId) || hosts[0];
      void connectHost(target);
    } else if (
      window.location.protocol.startsWith("http") &&
      window.location.hostname !== "localhost"
    ) {
      // Optionally show pairing modal when opened fresh.
      setPairOpen(false);
    }
    return () => {
      void termAttachRef.current?.close();
      clientRef.current?.close();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function connectHost(host: SavedHost, pwOverride?: string): Promise<void> {
    await termAttachRef.current?.close();
    termAttachRef.current = null;
    clientRef.current?.close();

    setConnState("connecting");
    setConnError("");

    const cli = new WadaClient({
      offer: host.offer,
      endpoint: host.offer ? undefined : host.directUrl,
      credential: host.token,
      password: pwOverride ?? pairPassword,
    });
    clientRef.current = cli;

    cli.onStatus((st, err) => {
      setConnState(st);
      if (err) setConnError(err);
    });

    cli.onEvent((ev: DaemonEvent) => {
      if (ev.kind === "worker.updated" && ev.worker) {
        const updated = ev.worker;
        setWorkers((prev) => {
          const rest = prev.filter((w) => w.id !== updated.id);
          return [updated, ...rest];
        });
      }
      if (ev.kind === "terminal.updated" && ev.terminal) {
        const t = ev.terminal;
        setTerminals((prev) => {
          const exists = prev.some((x) => x.id === t.id);
          return exists ? prev.map((x) => (x.id === t.id ? t : x)) : [...prev, t];
        });
      }
      if (!ev.session || ev.session !== selectedSessionRef.current) return;
      if (ev.kind === "turn.started") {
        setBusyTurn(true);
        setLiveDelta("");
      } else if (ev.kind === "text.delta" && ev.text?.delta) {
        setLiveDelta((d) => d + ev.text!.delta!);
      } else if (ev.kind === "approval.requested" && ev.approval) {
        setApprovals((prev) => [...prev, ev.approval!]);
      } else if (
        ev.kind === "turn.succeeded" ||
        ev.kind === "turn.failed" ||
        ev.kind === "turn.interrupted"
      ) {
        setBusyTurn(false);
        setLiveDelta("");
        if (ev.error) setConnError(ev.error);
        void refreshSession(cli, ev.session);
      }
    });

    try {
      await cli.connect();
      setE2eeMode(cli.mode);
      setConnState("connected");
      const [sessRes, harnessRes, termRes] = await Promise.all([
        cli.listSessions(),
        cli.listHarnesses().catch(() => [] as HarnessInfo[]),
        cli.listTerminals().catch(() => [] as TerminalInfo[]),
      ]);
      setSessions(sessRes.sessions || []);
      setHarnesses(harnessRes);
      setTerminals(termRes);
      if (sessRes.sessions?.length) {
        const first = sessRes.sessions[0];
        setSelectedSessionId(first.id);
        await refreshSession(cli, first.id);
      }
    } catch (err) {
      setConnState("disconnected");
      setConnError(err instanceof Error ? err.message : "Failed to connect");
    }
  }

  const selectedSessionRef = useRef(selectedSessionId);
  selectedSessionRef.current = selectedSessionId;

  async function refreshSession(cli: WadaClient, sessionId: string): Promise<void> {
    const [msgs, wks, chg] = await Promise.all([
      cli.getMessages(sessionId).catch(() => [] as SessionMessage[]),
      cli.listWorkers(sessionId).catch(() => [] as WorkerInfo[]),
      cli.getChanges(sessionId).catch(() => [] as FileChange[]),
    ]);
    setMessages(msgs);
    setWorkers(wks);
    setChanges(chg);
  }

  async function selectSession(id: string): Promise<void> {
    setSelectedSessionId(id);
    setApprovals([]);
    setLiveDelta("");
    setSidebarOpen(false);
    if (clientRef.current) {
      await refreshSession(clientRef.current, id);
    }
  }

  async function handlePairSubmit(e: React.FormEvent): Promise<void> {
    e.preventDefault();
    const target = pairInput.trim();
    if (!target) return;
    let host: SavedHost;
    if (target.includes("#offer=") || (!target.includes("://") && !target.includes(":"))) {
      const off = decodeOffer(target);
      host = {
        id: off.serverId || crypto.randomUUID(),
        name: pairName.trim() || off.hostname || off.serverId || "Remote Daemon",
        mode: off.relayUrl ? "relay" : "direct",
        offer: target,
        relayUrl: off.relayUrl,
        directUrl: off.directUrl,
        serverId: off.serverId,
        token: off.token,
        requiresPassword: off.requiresPassword,
        addedAt: Date.now(),
      };
    } else {
      host = {
        id: crypto.randomUUID(),
        name: pairName.trim() || target,
        mode: "direct",
        directUrl: target,
        addedAt: Date.now(),
      };
    }
    const next = upsertHost(host);
    setHosts(next);
    setActiveHostId(host.id);
    saveActiveHostId(host.id);
    setPairOpen(false);
    await connectHost(host, pairPassword);
  }

  async function handleSend(e: React.FormEvent): Promise<void> {
    e.preventDefault();
    const cli = clientRef.current;
    const text = promptText.trim();
    if (!cli || !text || connState !== "connected") return;

    setPromptText("");
    let sessId = selectedSessionId;
    if (!sessId) {
      const created = await cli.createSession(newDir.trim() || ".", newProvider, mode);
      sessId = created.id;
      setSessions((prev) => [created, ...prev]);
      setSelectedSessionId(sessId);
    }
    setMessages((prev) => [...prev, { id: "local_" + Date.now(), role: "user", text }]);
    setBusyTurn(true);
    await cli.sendMessage(sessId, text, mode);
  }

  async function handleCreateTerminal(): Promise<void> {
    const cli = clientRef.current;
    if (!cli) return;
    const activeSess = sessions.find((s) => s.id === selectedSessionId);
    const info = await cli.createTerminal(activeSess?.directory || newDir || ".");
    setTerminals((prev) => [...prev, info]);
    await attachTerminal(info.id);
  }

  async function attachTerminal(id: string): Promise<void> {
    const cli = clientRef.current;
    if (!cli) return;
    await termAttachRef.current?.close();
    setActiveTermId(id);
    setTermOutput("");
    const st = await cli.attachTerminal(id, (chunk) => {
      setTermOutput((prev) => formatTerminalOutput(prev, chunk));
    });
    termAttachRef.current = st;
    if (st.snapshot.length > 0) {
      setTermOutput(formatTerminalOutput("", st.snapshot));
    }
  }

  function handleSendTermCmd(e: React.FormEvent): void {
    e.preventDefault();
    if (!termAttachRef.current) return;
    termAttachRef.current.writeInput(new TextEncoder().encode(termCmd + "\r"));
    setTermCmd("");
  }

  const activeSession = sessions.find((s) => s.id === selectedSessionId);

  return (
    <div className="shell">
      <header className="topbar">
        <button className="icon-btn mobile-only" onClick={() => setSidebarOpen((v) => !v)}>
          ☰
        </button>
        <div className="brand">
          <span className="brand-mark">W</span>
          <strong>Wada</strong>
          {e2eeMode === "relay" && <span className="pill pill-green">E2EE Relay</span>}
          {e2eeMode === "direct" && <span className="pill">Direct WS</span>}
          <span className={`pill pill-${connState}`}>{connState}</span>
        </div>
        <div className="topbar-actions">
          <button className="btn btn-secondary" onClick={() => setPairOpen(true)}>
            + Pair / Host
          </button>
        </div>
      </header>

      <div className="workspace">
        <aside className={`sidebar ${sidebarOpen ? "open" : ""}`}>
          <div className="side-section">
            <div className="side-title">Hosts</div>
            {hosts.length === 0 ? (
              <p className="muted">No saved hosts. Pair via #offer= URL or direct WS.</p>
            ) : (
              hosts.map((h) => (
                <div key={h.id} className={`host-item ${h.id === activeHostId ? "active" : ""}`}>
                  <button
                    className="host-select"
                    onClick={() => {
                      setActiveHostId(h.id);
                      saveActiveHostId(h.id);
                      void connectHost(h);
                    }}
                  >
                    <span>{h.name}</span>
                    <small>{h.mode === "relay" ? "Noise E2EE" : h.directUrl}</small>
                  </button>
                  <button
                    className="icon-btn"
                    title="Remove"
                    onClick={() => setHosts(deleteHost(h.id))}
                  >
                    ×
                  </button>
                </div>
              ))
            )}
          </div>

          <div className="side-section grow">
            <div className="side-header">
              <span className="side-title">Sessions</span>
              <button
                className="btn btn-small"
                onClick={() => {
                  setSelectedSessionId("");
                  setMessages([]);
                  setChanges([]);
                  setWorkers([]);
                  setSidebarOpen(false);
                }}
              >
                + New
              </button>
            </div>
            <div className="session-list">
              {sessions.map((s) => (
                <button
                  key={s.id}
                  className={`session-item ${s.id === selectedSessionId ? "active" : ""}`}
                  onClick={() => void selectSession(s.id)}
                >
                  <div className="session-title">{s.title || "Untitled session"}</div>
                  <div className="session-dir">{s.directory}</div>
                </button>
              ))}
            </div>
          </div>
        </aside>

        <main className="main">
          {connError && <div className="banner-error">{connError}</div>}

          <div className="tabs">
            <button
              className={rightTab === "chat" ? "tab active" : "tab"}
              onClick={() => setRightTab("chat")}
            >
              Chat {workers.length > 0 ? `(${workers.length} workers)` : ""}
            </button>
            <button
              className={rightTab === "diffs" ? "tab active" : "tab"}
              onClick={() => setRightTab("diffs")}
            >
              Diffs ({changes.length})
            </button>
            <button
              className={rightTab === "terminal" ? "tab active" : "tab"}
              onClick={() => setRightTab("terminal")}
            >
              Terminal ({terminals.length})
            </button>
          </div>

          {rightTab === "chat" && (
            <div className="panel">
              {workers.length > 0 && (
                <div className="worker-strip">
                  {workers.map((w) => (
                    <span key={w.id} className="pill">
                      {w.name || w.provider}: {w.status} {w.branch ? `· ${w.branch}` : ""}
                    </span>
                  ))}
                </div>
              )}

              <div className="transcript">
                {!activeSession && messages.length === 0 && (
                  <div className="new-chat-box">
                    <h3>Start a coding task</h3>
                    <div className="row">
                      <input
                        value={newDir}
                        onChange={(e) => setNewDir(e.target.value)}
                        placeholder="Project directory"
                      />
                      <select value={newProvider} onChange={(e) => setNewProvider(e.target.value)}>
                        <option value="">PiG (Chief Agent)</option>
                        {harnesses
                          .filter((h) => h.id !== "pig")
                          .map((h) => (
                            <option key={h.id} value={h.id}>
                              {h.name} {!h.available ? "(unavailable)" : ""}
                            </option>
                          ))}
                      </select>
                      <select
                        value={mode}
                        onChange={(e) => setMode(e.target.value as "ask" | "full")}
                      >
                        <option value="ask">Ask</option>
                        <option value="full">Independent</option>
                      </select>
                    </div>
                  </div>
                )}

                {messages.map((m) => (
                  <div key={m.id} className={`msg msg-${m.role}`}>
                    <div className="msg-role">{m.role === "user" ? "You" : "Agent"}</div>
                    {m.text && <pre className="msg-text">{m.text}</pre>}
                    {m.parts?.map((p, idx) => (
                      <div key={idx}>
                        {p.kind === "text" && p.text && <pre className="msg-text">{p.text}</pre>}
                        {p.kind === "tool" && p.tool && (
                          <details className="tool-card">
                            <summary>
                              [{p.tool.status}] {p.tool.name}{" "}
                              {p.tool.command ? `— ${p.tool.command}` : ""}
                            </summary>
                            {p.tool.output?.length ? (
                              <pre className="tool-out">{p.tool.output.join("\n")}</pre>
                            ) : null}
                          </details>
                        )}
                      </div>
                    ))}
                  </div>
                ))}

                {liveDelta && (
                  <div className="msg msg-assistant">
                    <div className="msg-role">Agent (streaming)</div>
                    <pre className="msg-text">{liveDelta}</pre>
                  </div>
                )}

                {approvals.map((ap) => (
                  <div key={ap.id} className="approval-card">
                    <div>
                      <strong>Approval needed:</strong> {ap.action}
                    </div>
                    {ap.resources?.length ? (
                      <div className="muted">{ap.resources.join(", ")}</div>
                    ) : null}
                    <div className="row">
                      {(["once", "always", "reject"] as const).map((dec) => (
                        <button
                          key={dec}
                          className={dec === "reject" ? "btn btn-danger" : "btn btn-primary"}
                          onClick={async () => {
                            await clientRef.current?.replyApproval(ap.session, ap.id, dec);
                            setApprovals((prev) => prev.filter((x) => x.id !== ap.id));
                          }}
                        >
                          {dec === "once" ? "Allow once" : dec === "always" ? "Always" : "Reject"}
                        </button>
                      ))}
                    </div>
                  </div>
                ))}
              </div>

              <form className="composer" onSubmit={(e) => void handleSend(e)}>
                <input
                  value={promptText}
                  onChange={(e) => setPromptText(e.target.value)}
                  placeholder="Ask Wada or delegate to Claude Code / Codex workers…"
                  disabled={connState !== "connected"}
                />
                {busyTurn && selectedSessionId ? (
                  <button
                    type="button"
                    className="btn btn-danger"
                    onClick={() => void clientRef.current?.abortSession(selectedSessionId)}
                  >
                    Stop
                  </button>
                ) : (
                  <button
                    type="submit"
                    className="btn btn-primary"
                    disabled={connState !== "connected"}
                  >
                    Send
                  </button>
                )}
              </form>
            </div>
          )}

          {rightTab === "diffs" && (
            <div className="panel diffs">
              {changes.length === 0 ? (
                <p className="muted pad">No recorded file changes for this session.</p>
              ) : (
                changes.map((c) => (
                  <details key={c.path} open className="diff-card">
                    <summary>
                      <span>{c.path}</span>
                      <span className="muted">
                        +{c.added || 0} / -{c.removed || 0} ({c.status})
                      </span>
                    </summary>
                    <pre className="diff-patch">{c.patch || "(no patch text)"}</pre>
                  </details>
                ))
              )}
            </div>
          )}

          {rightTab === "terminal" && (
            <div className="panel terminal-pane">
              <div className="term-bar">
                {terminals.map((t) => (
                  <div key={t.id} className={`term-tab ${t.id === activeTermId ? "active" : ""}`}>
                    <button onClick={() => void attachTerminal(t.id)}>
                      {t.name || t.id} ({t.running ? "on" : "exit"})
                    </button>
                    <button
                      className="icon-btn"
                      onClick={async () => {
                        await clientRef.current?.killTerminal(t.id);
                        setTerminals((prev) => prev.filter((x) => x.id !== t.id));
                      }}
                    >
                      ×
                    </button>
                  </div>
                ))}
                <button className="btn btn-small" onClick={() => void handleCreateTerminal()}>
                  + PTY
                </button>
                <button
                  className="btn btn-small"
                  onClick={() =>
                    termAttachRef.current?.writeInput(new Uint8Array([0x03]))
                  }
                >
                  Ctrl+C
                </button>
              </div>
              <pre className="term-output">{termOutput || "Select or create a PTY terminal…"}</pre>
              <form className="composer" onSubmit={handleSendTermCmd}>
                <input
                  value={termCmd}
                  onChange={(e) => setTermCmd(e.target.value)}
                  placeholder="Command to send to active PTY…"
                  disabled={!activeTermId}
                />
                <button type="submit" className="btn btn-primary" disabled={!activeTermId}>
                  Run
                </button>
              </form>
            </div>
          )}
        </main>
      </div>

      {pairOpen && (
        <div className="modal-backdrop" onClick={() => setPairOpen(false)}>
          <form
            className="modal"
            onClick={(e) => e.stopPropagation()}
            onSubmit={(e) => void handlePairSubmit(e)}
          >
            <h3>Pair or Connect Daemon</h3>
            <label>
              Host name
              <input value={pairName} onChange={(e) => setPairName(e.target.value)} required />
            </label>
            <label>
              Pairing #offer= link or direct WS URL
              <textarea
                rows={3}
                value={pairInput}
                onChange={(e) => setPairInput(e.target.value)}
                placeholder="https://app.wada.dev/#offer=... or ws://100.64.0.2:6767"
                required
              />
            </label>
            <label>
              Daemon password (if enabled)
              <input
                type="password"
                value={pairPassword}
                onChange={(e) => setPairPassword(e.target.value)}
                placeholder="Optional"
              />
            </label>
            <div className="row end">
              <button type="button" className="btn btn-secondary" onClick={() => setPairOpen(false)}>
                Cancel
              </button>
              <button type="submit" className="btn btn-primary">
                Connect
              </button>
            </div>
          </form>
        </div>
      )}
    </div>
  );
}
