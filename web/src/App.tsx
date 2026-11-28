import { useEffect, useMemo, useRef, useState } from "react";
import { TerminalTextStream, appendTerminalOutput } from "./client/ansi";
import {
  type AgentProfile,
  type ApprovalItem,
  type DaemonEvent,
  type Delivery,
  EV_APPROVAL_REQUESTED,
  EV_INTERACTION_REQUESTED,
  EV_SESSION_RENAMED,
  EV_TERMINAL_UPDATED,
  EV_TEXT_DELTA,
  EV_TURN_FAILED,
  EV_TURN_INTERRUPTED,
  EV_TURN_STARTED,
  EV_TURN_SUCCEEDED,
  EV_WORKER_UPDATED,
  EV_SCHEDULE_UPDATED,
  EV_WORKSPACE_UPDATED,
  type FileChange,
  type HarnessInfo,
  type InteractionItem,
  type ProjectInfo,
  type Schedule,
  type SessionInfo,
  type SessionMessage,
  type TerminalInfo,
  type WorkerInfo,
  type WorkspaceInfo,
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
import { groupSessions, workspaceName } from "./client/workspaces";
import { dictationSupported, joinDictation, startDictation } from "./dictation";
import { NewWorkspace } from "./NewWorkspace";
import { applyUpdate, onUpdateReady } from "./pwa";
import { SchedulesPanel } from "./Schedules";
import { AddProject, RestoreWorkspace } from "./WorkspaceDialogs";
import { WorkspacePanel } from "./WorkspacePanel";
import { decodeOffer, normalizeDirectWSURL, parseHostTarget } from "./crypto/offer";

type WorkspaceAction =
  | { kind: "add" | "restore"; cli: WadaClient }
  | { kind: "create"; cli: WadaClient; project: ProjectInfo };

type RightTab = "chat" | "workspace" | "schedules" | "diffs" | "terminal";

// ShownApproval is an approval with the conversation that shows it: its own,
// or the parent of the worker that asked. Replies go to approval.session.
type ShownApproval = ApprovalItem & { shownIn: string };

type InteractionAnswer = Parameters<WadaClient["answerInteraction"]>[0];

const MAX_RECONNECT_DELAY = 30_000;

function errorText(err: unknown, fallback: string): string {
  return err instanceof Error ? err.message : fallback;
}

// An unknown outcome is reported, never retried: the user decides (AGENTS.md rule 5).
function unconfirmed(what: string, err: unknown): string {
  return `${what} was not confirmed (${errorText(err, "unknown error")}). It may still have taken effect; check before trying again.`;
}

export function App() {
  const clientRef = useRef<WadaClient | null>(null);
  const hostRef = useRef<SavedHost | null>(null);
  const termAttachRef = useRef<AttachedTerminal | null>(null);
  const termGenRef = useRef(0);
  // Passwords stay in memory, per host, and are never written to storage.
  const passwordsRef = useRef(new Map<string, string>());
  const reconnectTimerRef = useRef<number | null>(null);
  const reconnectAttemptsRef = useRef(0);

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
  const [pairRelayToken, setPairRelayToken] = useState("");
  const [pairMessage, setPairMessage] = useState("");
  const [sidebarOpen, setSidebarOpen] = useState(false);

  const [sessions, setSessions] = useState<SessionInfo[]>([]);
  const [projects, setProjects] = useState<ProjectInfo[]>([]);
  const [workspaces, setWorkspaces] = useState<WorkspaceInfo[]>([]);
  const groups = useMemo(() => groupSessions(sessions, projects, workspaces), [sessions, projects, workspaces]);
  const [harnesses, setHarnesses] = useState<HarnessInfo[]>([]);
  const [selectedSessionId, setSelectedSessionId] = useState("");
  const [messages, setMessages] = useState<SessionMessage[]>([]);
  const [liveDelta, setLiveDelta] = useState("");
  // Approvals, interactions and running turns are kept for every session, so
  // one that arrives while another session is open is still there to answer.
  const [approvals, setApprovals] = useState<ShownApproval[]>([]);
  const [interactions, setInteractions] = useState<InteractionItem[]>([]);
  const [busySessions, setBusySessions] = useState<ReadonlySet<string>>(() => new Set());
  const [workers, setWorkers] = useState<WorkerInfo[]>([]);
  const [changes, setChanges] = useState<FileChange[]>([]);
  const [rightTab, setRightTab] = useState<RightTab>("chat");

  const [promptText, setPromptText] = useState("");
  // Empty leaves the folder to the daemon: its inbox.
  const [newDir, setNewDir] = useState("");
  const [newProvider, setNewProvider] = useState("");
  // A new chat starts in this workspace, instead of newDir, when set.
  const [newWorkspaceId, setNewWorkspaceId] = useState("");
  const [profiles, setProfiles] = useState<AgentProfile[]>([]);
  const [newProfile, setNewProfile] = useState("");
  // The workspace the Workspace tab shows when the user picked one; else the open chat's.
  const [shownWorkspaceId, setShownWorkspaceId] = useState("");
  const [workspaceAction, setWorkspaceAction] = useState<WorkspaceAction | null>(null);
  const [schedules, setSchedules] = useState<Schedule[]>([]);
  const [dictating, setDictating] = useState(false);
  const stopDictationRef = useRef<(() => void) | null>(null);
  const canDictate = useMemo(() => dictationSupported(), []);

  // toggleDictation listens and puts what it hears after the draft.
  function toggleDictation(): void {
    if (stopDictationRef.current) {
      stopDictationRef.current();
      return;
    }
    const base = promptText;
    setDictating(true);
    stopDictationRef.current = startDictation(
      (heard) => setPromptText(joinDictation(base, heard)),
      (err) => {
        stopDictationRef.current = null;
        setDictating(false);
        if (err) setConnError(err);
      }
    );
  }
  const [mode, setMode] = useState<"ask" | "full">("ask");

  const [terminals, setTerminals] = useState<TerminalInfo[]>([]);
  const [activeTermId, setActiveTermId] = useState("");
  const [termOutput, setTermOutput] = useState("");
  const [termCmd, setTermCmd] = useState("");

  const selectedSessionRef = useRef(selectedSessionId);
  selectedSessionRef.current = selectedSessionId;
  const connStateRef = useRef(connState);
  connStateRef.current = connState;
  const [updateReady, setUpdateReady] = useState(false);
  const [online, setOnline] = useState(() => navigator.onLine);

  useEffect(() => onUpdateReady(setUpdateReady), []);

  // Back on the network, reconnect now rather than at the end of the backoff.
  useEffect(() => {
    const up = () => {
      setOnline(true);
      const host = hostRef.current;
      if (host && connStateRef.current === "disconnected") {
        reconnectAttemptsRef.current = 0;
        void connectHost(host);
      }
    };
    const down = () => setOnline(false);
    window.addEventListener("online", up);
    window.addEventListener("offline", down);
    return () => {
      window.removeEventListener("online", up);
      window.removeEventListener("offline", down);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Extract #offer= from initial URL fragment and wipe it from browser history.
  useEffect(() => {
    const hash = window.location.hash;
    if (hash.includes("#offer=")) {
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
        setHosts(upsertHost(host));
        void openHost(host);
      } catch (err) {
        setConnError(errorText(err, "Invalid pairing link"));
      }
    } else if (hosts.length > 0) {
      const target = hosts.find((h) => h.id === activeHostId) || hosts[0];
      void openHost(target);
    }
    return () => {
      cancelReconnect();
      hostRef.current = null;
      termAttachRef.current = null;
      clientRef.current?.close();
      clientRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function cancelReconnect(): void {
    if (reconnectTimerRef.current !== null) {
      window.clearTimeout(reconnectTimerRef.current);
      reconnectTimerRef.current = null;
    }
  }

  function scheduleReconnect(host: SavedHost): void {
    if (reconnectTimerRef.current !== null) return;
    const attempt = reconnectAttemptsRef.current++;
    const delay = Math.min(MAX_RECONNECT_DELAY, 1000 * 2 ** attempt);
    reconnectTimerRef.current = window.setTimeout(() => {
      reconnectTimerRef.current = null;
      if (hostRef.current?.id === host.id) void connectHost(host);
    }, delay);
  }

  function openPairFor(host: SavedHost): void {
    setPairName(host.name);
    setPairInput(host.offer || host.directUrl || "");
    setPairMessage("");
    setPairPassword("");
    setPairRelayToken(host.relayToken || "");
    setPairOpen(true);
  }

  // openHost is a connection the user asked for: it starts the backoff afresh.
  async function openHost(host: SavedHost, password?: string): Promise<void> {
    setActiveHostId(host.id);
    saveActiveHostId(host.id);
    if (password !== undefined) passwordsRef.current.set(host.id, password);
    if (host.requiresPassword && !passwordsRef.current.get(host.id)) {
      openPairFor(host);
      return;
    }
    reconnectAttemptsRef.current = 0;
    await connectHost(host);
  }

  function setBusy(session: string, busy: boolean): void {
    setBusySessions((prev) => {
      if (prev.has(session) === busy) return prev;
      const next = new Set(prev);
      if (busy) next.add(session);
      else next.delete(session);
      return next;
    });
  }

  function chooseSession(id: string): void {
    selectedSessionRef.current = id;
    setSelectedSessionId(id);
    setLiveDelta("");
  }

  function resetHostState(): void {
    termGenRef.current++;
    termAttachRef.current = null;
    setSessions([]);
    setProjects([]);
    setWorkspaces([]);
    setNewWorkspaceId("");
    setShownWorkspaceId("");
    setMessages([]);
    setApprovals([]);
    setInteractions([]);
    setBusySessions(new Set());
    setWorkers([]);
    setChanges([]);
    setTerminals([]);
    setActiveTermId("");
    setTermOutput("");
    setLiveDelta("");
  }

  async function connectHost(host: SavedHost): Promise<void> {
    cancelReconnect();
    const sameHost = hostRef.current?.id === host.id;
    setWorkspaceAction(null);
    hostRef.current = host;
    // Closing the old connection also detaches its terminals on the daemon.
    clientRef.current?.close();
    termGenRef.current++;
    termAttachRef.current = null;
    // The daemon does not say which turns are running, and one may have ended
    // while disconnected; a stuck Stop button is worse than a missing one.
    setBusySessions(new Set());
    if (!sameHost) {
      chooseSession("");
      resetHostState();
    }

    setConnState("connecting");
    setConnError("");

    const cli = new WadaClient({
      offer: host.offer,
      endpoint: host.offer ? undefined : host.directUrl,
      credential: host.token,
      password: passwordsRef.current.get(host.id) || "",
      relayAuthToken: host.relayToken,
    });
    clientRef.current = cli;
    // A connection that was replaced must not touch the UI any more.
    const current = () => clientRef.current === cli;

    cli.onStatus((st, err) => {
      if (!current()) return;
      setConnState(st);
      if (err) setConnError(err);
      if (st === "disconnected") scheduleReconnect(host);
    });
    let workspaceUpdates: DaemonEvent[] | null = [];
    cli.onEvent((ev) => {
      if (!current()) return;
      if (ev.kind === EV_WORKSPACE_UPDATED && workspaceUpdates) {
        workspaceUpdates.push(ev);
      } else {
        handleEvent(cli, ev);
      }
    });

    try {
      await cli.connect();
      if (!current()) return;
      reconnectAttemptsRef.current = 0;
      setE2eeMode(cli.mode);
      setConnState("connected");
      const [sessRes, projRes, spaceRes, harnessRes, termRes, pendingRes] = await Promise.all([
        cli.listSessions(),
        // An older daemon without workspaces lists every session as Other.
        cli.listProjects().catch(() => [] as ProjectInfo[]),
        cli.listWorkspaces(true).catch(() => [] as WorkspaceInfo[]),
        cli.listHarnesses().catch(() => [] as HarnessInfo[]),
        cli.listTerminals().catch(() => [] as TerminalInfo[]),
        cli.pendingApprovals().catch(() => null),
      ]);
      void cli
        .listSchedules()
        .then((list) => {
          if (current()) setSchedules(list || []);
        })
        .catch(() => {});
      if (!current()) return;
      if (pendingRes) {
        // Approvals asked while this client was away are only here.
        setApprovals(pendingRes.map((p) => ({ ...p.approval, shownIn: p.shownIn || p.approval.session })));
      }
      const list = sessRes.sessions || [];
      setSessions(list);
      setProjects(projRes || []);
      setWorkspaces(spaceRes || []);
      for (const ev of workspaceUpdates) handleEvent(cli, ev);
      workspaceUpdates = null;
      setHarnesses(harnessRes || []);
      setTerminals(termRes || []);
      setActiveTermId("");
      setTermOutput("");
      const keep = list.find((s) => s.id === selectedSessionRef.current) || list[0];
      if (keep) {
        chooseSession(keep.id);
        await refreshSession(cli, keep.id);
      }
    } catch (err) {
      if (!current()) return;
      const msg = errorText(err, "Failed to connect");
      setConnState("disconnected");
      setConnError(msg);
      if (msg.startsWith("unauthorized")) {
        // A wrong password will not start working by itself: ask for it.
        passwordsRef.current.delete(host.id);
        openPairFor(host);
        setPairMessage(`${host.name} refused the connection. Enter its password, or pair again with a new link.`);
        return;
      }
      scheduleReconnect(host);
    }
  }

  function handleEvent(cli: WadaClient, ev: DaemonEvent): void {
    switch (ev.kind) {
      case EV_WORKER_UPDATED: {
        const updated = ev.worker;
        // The strip shows the open conversation's workers only.
        if (!updated || updated.parentSession !== selectedSessionRef.current) return;
        setWorkers((prev) => [updated, ...prev.filter((w) => w.id !== updated.id)]);
        return;
      }
      case EV_TERMINAL_UPDATED: {
        const t = ev.terminal;
        if (!t) return;
        setTerminals((prev) =>
          prev.some((x) => x.id === t.id) ? prev.map((x) => (x.id === t.id ? t : x)) : [...prev, t]
        );
        return;
      }
      case EV_WORKSPACE_UPDATED: {
        // The project or workspace as it now is, archived ones included.
        const { project, workspace } = ev;
        if (project) setProjects((prev) => [...prev.filter((p) => p.id !== project.id), project]);
        if (workspace) setWorkspaces((prev) => [...prev.filter((w) => w.id !== workspace.id), workspace]);
        return;
      }
      case EV_SCHEDULE_UPDATED: {
        const sc = ev.schedule;
        if (sc) applySchedule(sc);
        return;
      }
      case EV_SESSION_RENAMED: {
        const { session, title } = ev;
        if (!session || title === undefined) return;
        setSessions((prev) => prev.map((s) => (s.id === session ? { ...s, title } : s)));
        return;
      }
      case EV_APPROVAL_REQUESTED: {
        const ap = ev.approval;
        if (!ap) return;
        // A worker's approval arrives on its parent's session.
        const shown: ShownApproval = { ...ap, shownIn: ev.session || ap.session };
        setApprovals((prev) => (prev.some((x) => x.id === ap.id) ? prev : [...prev, shown]));
        return;
      }
      case EV_INTERACTION_REQUESTED: {
        const it = ev.interaction;
        if (!it) return;
        setInteractions((prev) => {
          const rest = prev.filter((x) => x.id !== it.id);
          return it.settled ? rest : [...rest, it];
        });
        return;
      }
      case EV_TURN_STARTED:
        if (!ev.session) return;
        setBusy(ev.session, true);
        if (ev.session === selectedSessionRef.current) setLiveDelta("");
        return;
      case EV_TEXT_DELTA: {
        const delta = ev.text?.delta;
        if (delta && ev.session === selectedSessionRef.current) setLiveDelta((d) => d + delta);
        return;
      }
      case EV_TURN_SUCCEEDED:
      case EV_TURN_FAILED:
      case EV_TURN_INTERRUPTED: {
        const session = ev.session;
        if (!session) return;
        setBusy(session, false);
        // Nothing waits on a finished turn's approvals any more.
        setApprovals((prev) => prev.filter((a) => a.session !== session));
        if (session !== selectedSessionRef.current) return;
        setLiveDelta("");
        if (ev.error) setConnError(ev.error);
        void refreshSession(cli, session);
        return;
      }
    }
  }

  async function refreshSession(cli: WadaClient, sessionId: string): Promise<void> {
    const [msgs, wks, chg, pending] = await Promise.all([
      cli.getMessages(sessionId).catch(() => [] as SessionMessage[]),
      cli.listWorkers(sessionId).catch(() => [] as WorkerInfo[]),
      cli.getChanges(sessionId).catch(() => [] as FileChange[]),
      cli.pendingInteractions(sessionId).catch(() => null),
    ]);
    if (clientRef.current !== cli) return;
    if (pending) {
      setInteractions((prev) => [
        ...prev.filter((x) => x.session !== sessionId),
        ...pending.filter((x) => !x.settled),
      ]);
    }
    // The user may have moved to another session while this loaded.
    if (selectedSessionRef.current !== sessionId) return;
    setMessages(msgs || []);
    setWorkers(wks || []);
    setChanges(chg || []);
  }

  async function selectSession(id: string): Promise<void> {
    chooseSession(id);
    setShownWorkspaceId("");
    setNewWorkspaceId("");
    setSidebarOpen(false);
    if (clientRef.current) {
      await refreshSession(clientRef.current, id);
    }
  }

  function handlePairSubmit(e: React.FormEvent): void {
    e.preventDefault();
    const target = pairInput.trim();
    if (!target) return;
    const relayToken = pairRelayToken.trim() || undefined;
    let host: SavedHost;
    try {
      const parsed = parseHostTarget(target);
      if ("offer" in parsed) {
        const off = decodeOffer(parsed.offer);
        host = {
          id: off.serverId || crypto.randomUUID(),
          name: pairName.trim() || off.hostname || off.serverId || "Remote Daemon",
          mode: off.relayUrl ? "relay" : "direct",
          offer: parsed.offer,
          relayUrl: off.relayUrl,
          directUrl: off.directUrl,
          serverId: off.serverId,
          token: off.token,
          relayToken,
          requiresPassword: off.requiresPassword,
          addedAt: Date.now(),
        };
      } else {
        // The same endpoint is the same host, so re-pairing replaces it.
        host = {
          id: "direct:" + normalizeDirectWSURL(parsed.endpoint),
          name: pairName.trim() || target,
          mode: "direct",
          directUrl: parsed.endpoint,
          // Ask again after a reload, since the password is never stored.
          requiresPassword: pairPassword !== "",
          addedAt: Date.now(),
        };
      }
    } catch (err) {
      setConnError(errorText(err, "Invalid pairing link"));
      return;
    }
    const password = pairPassword;
    setPairPassword("");
    setHosts(upsertHost(host));
    setPairOpen(false);
    void openHost(host, password);
  }

  // handleSend sends the composer's text; during a turn it steers the
  // turn unless deliver says to queue it or interrupt the turn.
  async function handleSend(e: React.FormEvent | null, deliver?: Delivery): Promise<void> {
    e?.preventDefault();
    const cli = clientRef.current;
    const text = promptText.trim();
    if (!cli || !text || connState !== "connected") return;

    setPromptText("");
    let sessId = selectedSessionId;
    const during = sessId !== "" && busySessions.has(sessId);
    const localId = "local_" + crypto.randomUUID();
    try {
      if (!sessId) {
        const created = await cli.createSession(newDir.trim(), newProvider, mode, {
          workspace: newWorkspaceId,
          profile: newProfile,
        });
        sessId = created.id;
        setSessions((prev) => [created, ...prev]);
        chooseSession(sessId);
        setNewWorkspaceId("");
        setNewProfile("");
      }
      setMessages((prev) => [...prev, { id: localId, role: "user", text }]);
      setBusy(sessId, true);
      await cli.sendMessage(sessId, text, mode, during ? deliver || "steer" : undefined);
    } catch (err) {
      setConnError(unconfirmed(sessId ? "The message" : "The new session", err));
      setPromptText((cur) => cur || text);
      setMessages((prev) => prev.filter((m) => m.id !== localId));
      if (sessId) {
        setBusy(sessId, false);
        void refreshSession(cli, sessId);
      }
    }
  }

  // removeHost forgets a saved host, and disconnects from it when it is the
  // one in use.
  function removeHost(h: SavedHost): void {
    passwordsRef.current.delete(h.id);
    setHosts(deleteHost(h.id));
    if (hostRef.current?.id !== h.id && activeHostId !== h.id) return;
    cancelReconnect();
    hostRef.current = null;
    setWorkspaceAction(null);
    clientRef.current?.close();
    clientRef.current = null;
    setActiveHostId("");
    saveActiveHostId("");
    chooseSession("");
    resetHostState();
    setConnState("disconnected");
    setE2eeMode("");
  }

  async function handleAbort(session: string): Promise<void> {
    try {
      await clientRef.current?.abortSession(session);
    } catch (err) {
      setConnError(unconfirmed("Stopping the turn", err));
    }
  }

  async function handleApproval(
    ap: ApprovalItem,
    decision: "once" | "always" | "reject"
  ): Promise<void> {
    try {
      await clientRef.current?.replyApproval(ap.session, ap.id, decision);
      setApprovals((prev) => prev.filter((x) => x.id !== ap.id));
    } catch (err) {
      setConnError(unconfirmed("The approval reply", err));
    }
  }

  async function handleAnswer(ans: InteractionAnswer): Promise<void> {
    try {
      await clientRef.current?.answerInteraction(ans);
      setInteractions((prev) => prev.filter((x) => x.id !== ans.id));
    } catch (err) {
      setConnError(unconfirmed("The answer", err));
    }
  }

  async function handleCreateTerminal(): Promise<void> {
    const cli = clientRef.current;
    if (!cli) return;
    const activeSess = sessions.find((s) => s.id === selectedSessionId);
    let info: TerminalInfo;
    try {
      info = await cli.createTerminal(activeSess?.directory || newDir.trim());
    } catch (err) {
      setConnError(unconfirmed("Creating the terminal", err));
      return;
    }
    setTerminals((prev) => (prev.some((t) => t.id === info.id) ? prev : [...prev, info]));
    await attachTerminal(info.id);
  }

  async function attachTerminal(id: string): Promise<void> {
    const cli = clientRef.current;
    if (!cli) return;
    // Each attach gets a generation; output and results of a replaced one are dropped.
    const gen = ++termGenRef.current;
    const prev = termAttachRef.current;
    termAttachRef.current = null;
    setActiveTermId(id);
    setTermOutput("");
    await prev?.close();
    if (gen !== termGenRef.current) return;
    const stream = new TerminalTextStream();
    try {
      const st = await cli.attachTerminal(id, (chunk) => {
        if (gen !== termGenRef.current) return;
        const text = stream.push(chunk);
        if (text) setTermOutput((out) => appendTerminalOutput(out, text));
      });
      if (gen !== termGenRef.current) {
        void st.close();
        return;
      }
      termAttachRef.current = st;
    } catch (err) {
      if (gen === termGenRef.current) setConnError(errorText(err, "Failed to attach terminal"));
    }
  }

  async function handleKillTerminal(id: string): Promise<void> {
    try {
      await clientRef.current?.killTerminal(id);
    } catch (err) {
      setConnError(unconfirmed("Killing the terminal", err));
      return;
    }
    setTerminals((prev) => prev.filter((x) => x.id !== id));
    if (id === activeTermId) {
      termGenRef.current++;
      termAttachRef.current = null;
      setActiveTermId("");
      setTermOutput("");
    }
  }

  function handleSendTermCmd(e: React.FormEvent): void {
    e.preventDefault();
    const term = termAttachRef.current;
    if (!term) return;
    try {
      term.writeInput(new TextEncoder().encode(termCmd + "\r"));
      setTermCmd("");
    } catch (err) {
      setConnError(errorText(err, "Terminal input failed"));
    }
  }

  const activeSession = sessions.find((s) => s.id === selectedSessionId);
  const activeWorkspaces = workspaces.filter((w) => !w.archived);
  const newWorkspace = activeWorkspaces.find((w) => w.id === newWorkspaceId);
  const sessionWorkspace =
    activeSession &&
    (activeWorkspaces.find((w) => w.id === activeSession.workspace) ||
      activeWorkspaces.find((w) => w.directory === activeSession.directory));
  const shownWorkspace =
    activeWorkspaces.find((w) => w.id === shownWorkspaceId) || sessionWorkspace || newWorkspace;
  const shownProject = shownWorkspace && projects.find((p) => p.id === shownWorkspace.project);
  const directHost = (() => {
    const url = hostRef.current?.directUrl;
    if (!url) return "";
    try {
      return new URL(url.includes("://") ? url : "ws://" + url).hostname;
    } catch {
      return "";
    }
  })();

  // applySchedule takes in a schedule the daemon added, changed, ran or removed.
  function applySchedule(sc: Schedule): void {
    setSchedules((prev) => {
      const rest = prev.filter((x) => x.id !== sc.id);
      return sc.removed ? rest : [...rest, sc].sort((a, b) => a.name.localeCompare(b.name));
    });
  }

  const workspaceClient = connState === "connected" ? clientRef.current : null;
  const action = workspaceAction?.cli === workspaceClient ? workspaceAction : null;
  const closeWorkspaceAction = () => setWorkspaceAction(null);

  function workspaceReady(cli: WadaClient, w: WorkspaceInfo, project?: ProjectInfo): void {
    if (clientRef.current !== cli) return;
    if (project) setProjects((prev) => [...prev.filter((p) => p.id !== project.id), project]);
    setWorkspaces((prev) => [...prev.filter((x) => x.id !== w.id), w]);
    closeWorkspaceAction();
    startChatIn(w);
  }

  function startChatIn(w: WorkspaceInfo): void {
    chooseSession("");
    setMessages([]);
    setChanges([]);
    setWorkers([]);
    setNewWorkspaceId(w.id);
    setShownWorkspaceId("");
    setRightTab("chat");
    setSidebarOpen(false);
  }

  function showWorkspace(w: WorkspaceInfo): void {
    setShownWorkspaceId(w.id);
    setRightTab("workspace");
    setSidebarOpen(false);
  }

  function openTerminal(id: string): void {
    setRightTab("terminal");
    void attachTerminal(id);
  }
  const busyTurn = selectedSessionId !== "" && busySessions.has(selectedSessionId);
  const sessionApprovals = approvals.filter((a) => a.shownIn === selectedSessionId);
  const sessionInteractions = interactions.filter((x) => x.session === selectedSessionId);
  const needsAttention = (id: string) =>
    approvals.some((a) => a.shownIn === id) || interactions.some((x) => x.session === id);

  const newChatDir = newWorkspace?.directory || newDir.trim();
  useEffect(() => {
    const cli = clientRef.current;
    if (!cli || connState !== "connected" || selectedSessionId) return;
    let stale = false;
    const t = window.setTimeout(() => {
      cli
        .listProfiles(newChatDir)
        .catch(() => [] as AgentProfile[])
        .then((list) => {
          if (stale) return;
          setProfiles(list || []);
          setNewProfile((cur) => (list?.some((p) => p.name === cur) ? cur : ""));
        });
    }, 300);
    return () => {
      stale = true;
      window.clearTimeout(t);
    };
  }, [connState, selectedSessionId, newChatDir]);

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
        <div className="row">
          <button
            className="btn"
            onClick={() => {
              setPairName("My Workstation");
              setPairInput("");
              setPairPassword("");
              setPairRelayToken("");
              setPairMessage("");
              setPairOpen(true);
            }}
          >
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
                  <button className="host-select" onClick={() => void openHost(h)}>
                    <span>{h.name}</span>
                    <small>{h.mode === "relay" ? "Noise E2EE" : h.directUrl}</small>
                  </button>
                  <button
                    className="icon-btn"
                    title="Remove"
                    onClick={() => removeHost(h)}
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
                  chooseSession("");
                  setMessages([]);
                  setChanges([]);
                  setWorkers([]);
                  setNewWorkspaceId("");
                  setSidebarOpen(false);
                }}
              >
                + New
              </button>
            </div>
            <div className="row workspace-actions">
              <button
                className="btn btn-small"
                disabled={!workspaceClient}
                onClick={() => workspaceClient && setWorkspaceAction({ kind: "add", cli: workspaceClient })}
              >Add project</button>
              <button
                className="btn btn-small"
                disabled={!workspaceClient}
                onClick={() => workspaceClient && setWorkspaceAction({ kind: "restore", cli: workspaceClient })}
              >Restore workspace</button>
            </div>
            <div className="session-list">
              {groups.map((g) => (
                <div key={g.project?.id || "other"} className="project-group">
                  <div className="project-name" title={g.project?.root}>
                    <span>{g.name}</span>
                    {g.project && !g.project.inbox && (
                      <button
                        className="icon-btn"
                        title={`New workspace in ${g.name}`}
                        disabled={!workspaceClient}
                        onClick={() => workspaceClient && setWorkspaceAction({ kind: "create", project: g.project!, cli: workspaceClient })}
                      >
                        ＋
                      </button>
                    )}
                  </div>
                  {g.workspaces.map((w) => (
                    <div key={w.workspace?.id || "other"} className="workspace-group">
                      {w.workspace && (
                        <div
                          className={`workspace-name ${shownWorkspace?.id === w.workspace.id ? "active" : ""}`}
                          title={w.workspace.directory}
                        >
                          <button className="ws-select" onClick={() => showWorkspace(w.workspace!)}>
                            {w.name}
                            {w.workspace.pr ? <span className="pill">#{w.workspace.pr}</span> : null}
                          </button>
                          <button
                            className="icon-btn"
                            title={`New chat in ${w.name}`}
                            onClick={() => startChatIn(w.workspace!)}
                          >
                            ✎
                          </button>
                        </div>
                      )}
                      {w.sessions.map((s) => (
                        <button
                          key={s.id}
                          className={`session-item ${s.id === selectedSessionId ? "active" : ""}`}
                          onClick={() => void selectSession(s.id)}
                        >
                          <div className="session-title">
                            {s.title || "Untitled session"}{" "}
                            {needsAttention(s.id) && <span className="pill pill-attention">needs you</span>}
                            {busySessions.has(s.id) && <span className="pill">running</span>}
                          </div>
                          {!w.workspace && <div className="session-dir">{s.directory}</div>}
                        </button>
                      ))}
                    </div>
                  ))}
                </div>
              ))}
            </div>
          </div>
        </aside>

        <main className="main">
          {updateReady && (
            <div className="banner-info">
              A new version of Wada is ready.
              <button className="btn btn-small btn-primary" onClick={applyUpdate}>
                Reload
              </button>
            </div>
          )}
          {!online && <div className="banner-info">You're offline. Wada reconnects when the network is back.</div>}
          {connError && <div className="banner-error">{connError}</div>}

          <div className="tabs">
            <button
              className={rightTab === "chat" ? "tab active" : "tab"}
              onClick={() => setRightTab("chat")}
            >
              Chat {workers.length > 0 ? `(${workers.length} workers)` : ""}
            </button>
            <button
              className={rightTab === "workspace" ? "tab active" : "tab"}
              onClick={() => setRightTab("workspace")}
              disabled={!shownWorkspace}
            >
              Workspace
            </button>
            <button
              className={rightTab === "schedules" ? "tab active" : "tab"}
              onClick={() => setRightTab("schedules")}
            >
              Schedules ({schedules.length})
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
                      {newWorkspace ? (
                        <span className="pill pill-green">
                          In {workspaceName(newWorkspace, projects.find((p) => p.id === newWorkspace.project))}
                          <button className="icon-btn" title="Choose a folder instead" onClick={() => setNewWorkspaceId("")}>
                            ×
                          </button>
                        </span>
                      ) : (
                        <input
                          value={newDir}
                          onChange={(e) => setNewDir(e.target.value)}
                          placeholder="Project folder on the daemon (empty: its inbox)"
                        />
                      )}
                      {profiles.length > 0 && (
                        <select
                          value={newProfile}
                          onChange={(e) => {
                            const name = e.target.value;
                            setNewProfile(name);
                            const prof = profiles.find((p) => p.name === name);
                            if (prof) setNewProvider(prof.provider && prof.provider !== "pig" ? prof.provider : "");
                          }}
                          title="Agent profile"
                        >
                          <option value="">No profile</option>
                          {profiles.map((p) => (
                            <option key={p.name} value={p.name} title={p.description}>
                              {p.name}
                              {p.source === "project" ? " (project)" : ""}
                            </option>
                          ))}
                        </select>
                      )}
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
                  <div className="msg">
                    <div className="msg-role">Agent (streaming)</div>
                    <pre className="msg-text">{liveDelta}</pre>
                  </div>
                )}

                {sessionApprovals.map((ap) => (
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
                          onClick={() => void handleApproval(ap, dec)}
                        >
                          {dec === "once" ? "Allow once" : dec === "always" ? "Always" : "Reject"}
                        </button>
                      ))}
                    </div>
                  </div>
                ))}

                {sessionInteractions.map((it) => (
                  <InteractionCard key={it.id} item={it} onAnswer={handleAnswer} />
                ))}
              </div>

              <form className="composer" onSubmit={(e) => void handleSend(e)}>
                <input
                  value={promptText}
                  onChange={(e) => setPromptText(e.target.value)}
                  placeholder={
                    busyTurn
                      ? "Steer the agent, or queue a message for after this turn…"
                      : "Ask Wada or delegate to Claude Code / Codex workers…"
                  }
                  disabled={connState !== "connected"}
                />
                {canDictate && (
                  <button
                    type="button"
                    className={dictating ? "btn btn-danger" : "btn"}
                    title={dictating ? "Stop dictating" : "Dictate"}
                    aria-label={dictating ? "Stop dictating" : "Dictate"}
                    disabled={connState !== "connected"}
                    onClick={toggleDictation}
                  >
                    {dictating ? "■" : "🎤"}
                  </button>
                )}
                {busyTurn ? (
                  <>
                    <button
                      type="submit"
                      className="btn btn-primary"
                      title="Send into the running turn"
                      disabled={connState !== "connected" || !promptText.trim()}
                    >
                      Steer
                    </button>
                    <button
                      type="button"
                      className="btn"
                      title="Send when this turn ends"
                      disabled={connState !== "connected" || !promptText.trim()}
                      onClick={() => void handleSend(null, "queue")}
                    >
                      Queue
                    </button>
                    <button
                      type="button"
                      className="btn"
                      title="Stop this turn and send"
                      disabled={connState !== "connected" || !promptText.trim()}
                      onClick={() => void handleSend(null, "interrupt")}
                    >
                      Interrupt
                    </button>
                    <button
                      type="button"
                      className="btn btn-danger"
                      onClick={() => void handleAbort(selectedSessionId)}
                    >
                      Stop
                    </button>
                  </>
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

          {rightTab === "workspace" && shownWorkspace && clientRef.current && (
            <WorkspacePanel
              cli={clientRef.current}
              workspace={shownWorkspace}
              project={shownProject}
              terminals={terminals}
              host={directHost}
              onTerminal={openTerminal}
              onError={setConnError}
            />
          )}

          {rightTab === "schedules" && clientRef.current && (
            <SchedulesPanel
              cli={clientRef.current}
              schedules={schedules}
              session={selectedSessionId}
              sessionTitle={activeSession?.title || ""}
              directory={activeSession ? (sessionWorkspace ? sessionWorkspace.directory : activeSession.directory) : newChatDir}
              workspace={activeSession ? sessionWorkspace?.id || "" : newWorkspaceId}
              onChanged={applySchedule}
              onError={setConnError}
            />
          )}

          {rightTab === "workspace" && !shownWorkspace && (
            <div className="panel">
              <p className="muted pad">Pick a workspace in the sidebar.</p>
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
            <div className="panel">
              <div className="term-bar">
                {terminals.map((t) => (
                  <div key={t.id} className={`term-tab ${t.id === activeTermId ? "active" : ""}`}>
                    <button onClick={() => void attachTerminal(t.id)}>
                      {t.name || t.id} ({t.running ? "on" : "exit"})
                    </button>
                    <button className="icon-btn" onClick={() => void handleKillTerminal(t.id)}>
                      ×
                    </button>
                  </div>
                ))}
                <button className="btn btn-small" onClick={() => void handleCreateTerminal()}>
                  + PTY
                </button>
                <button
                  className="btn btn-small"
                  onClick={() => {
                    try {
                      termAttachRef.current?.writeInput(new Uint8Array([0x03]));
                    } catch (err) {
                      setConnError(errorText(err, "Terminal input failed"));
                    }
                  }}
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

      {action?.kind === "create" && (
        <NewWorkspace
          cli={action.cli}
          project={action.project}
          workspaces={workspaces}
          onClose={closeWorkspaceAction}
          onCreated={(w) => workspaceReady(action.cli, w)}
        />
      )}
      {action?.kind === "add" && (
        <AddProject
          cli={action.cli}
          host={hostRef.current?.name || "the daemon host"}
          onClose={closeWorkspaceAction}
          onAdded={(p, w) => workspaceReady(action.cli, w, p)}
        />
      )}
      {action?.kind === "restore" && (
        <RestoreWorkspace
          cli={action.cli}
          projects={projects}
          workspaces={workspaces}
          onClose={closeWorkspaceAction}
          onRestored={(w) => workspaceReady(action.cli, w)}
        />
      )}

      {pairOpen && (
        <div className="modal-backdrop" onClick={() => setPairOpen(false)}>
          <form className="modal" onClick={(e) => e.stopPropagation()} onSubmit={handlePairSubmit}>
            <h3>Pair or Connect Daemon</h3>
            {pairMessage && <p className="muted">{pairMessage}</p>}
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
                placeholder="https://app.wada.dev/#offer=... or my-mac.tailnet.ts.net"
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
            <label>
              Relay token (if the relay requires one)
              <input
                type="password"
                value={pairRelayToken}
                onChange={(e) => setPairRelayToken(e.target.value)}
                placeholder="Optional"
              />
            </label>
            <div className="row end">
              <button type="button" className="btn" onClick={() => setPairOpen(false)}>
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

// InteractionCard asks the user a question the agent is waiting on: a
// confirmation, a choice, free text, or a set of structured questions.
function InteractionCard({
  item,
  onAnswer,
}: {
  item: InteractionItem;
  onAnswer: (ans: InteractionAnswer) => Promise<void>;
}) {
  const [value, setValue] = useState(item.prefill ?? "");
  const [picked, setPicked] = useState<Record<string, string[]>>({});
  const [custom, setCustom] = useState<Record<string, string>>({});
  const [sending, setSending] = useState(false);
  const base = { session: item.session, id: item.id };

  async function send(ans: InteractionAnswer): Promise<void> {
    setSending(true);
    try {
      await onAnswer(ans);
    } finally {
      setSending(false);
    }
  }

  function toggle(key: string, option: string, multiple: boolean): void {
    setPicked((prev) => {
      const cur = prev[key] ?? [];
      const has = cur.includes(option);
      const next = multiple
        ? has
          ? cur.filter((v) => v !== option)
          : [...cur, option]
        : has
          ? []
          : [option];
      return { ...prev, [key]: next };
    });
  }

  let body: React.ReactNode;
  if (item.questions?.length) {
    const submitQuestions = () => {
      const answers: Record<string, string[]> = {};
      for (const q of item.questions ?? []) {
        const vals = [...(picked[q.key] ?? [])];
        const text = (custom[q.key] ?? "").trim();
        if (text) vals.push(text);
        answers[q.key] = vals;
      }
      void send({ ...base, answers });
    };
    body = (
      <>
        {item.questions.map((q) => (
          <div key={q.key}>
            <div>{q.title}</div>
            {q.description && <div className="muted">{q.description}</div>}
            <div className="row">
              {q.options?.map((o) => (
                <button
                  key={o.value}
                  type="button"
                  title={o.description}
                  className={(picked[q.key] ?? []).includes(o.value) ? "btn btn-primary" : "btn"}
                  onClick={() => toggle(q.key, o.value, q.multiple ?? false)}
                >
                  {o.label}
                </button>
              ))}
            </div>
            {(q.custom || !q.options?.length) && (
              <input
                value={custom[q.key] ?? ""}
                onChange={(e) => setCustom((prev) => ({ ...prev, [q.key]: e.target.value }))}
                placeholder="Your answer"
              />
            )}
          </div>
        ))}
        <div className="row">
          <button className="btn btn-primary" disabled={sending} onClick={submitQuestions}>
            Answer
          </button>
        </div>
      </>
    );
  } else if (item.kind === "confirm") {
    body = (
      <div className="row">
        <button
          className="btn btn-primary"
          disabled={sending}
          onClick={() => void send({ ...base, confirmed: true })}
        >
          Yes
        </button>
        <button
          className="btn"
          disabled={sending}
          onClick={() => void send({ ...base, confirmed: false })}
        >
          No
        </button>
      </div>
    );
  } else if (item.kind === "select") {
    body = (
      <div className="row">
        {item.options?.map((o) => (
          <button
            key={o}
            className="btn btn-primary"
            disabled={sending}
            onClick={() => void send({ ...base, value: o })}
          >
            {o}
          </button>
        ))}
      </div>
    );
  } else {
    body = (
      <>
        {item.kind === "editor" ? (
          <textarea rows={6} value={value} onChange={(e) => setValue(e.target.value)} />
        ) : (
          <input
            value={value}
            onChange={(e) => setValue(e.target.value)}
            placeholder={item.placeholder}
          />
        )}
        <div className="row">
          <button
            className="btn btn-primary"
            disabled={sending}
            onClick={() => void send({ ...base, value })}
          >
            Submit
          </button>
        </div>
      </>
    );
  }

  return (
    <div className="approval-card">
      <div>
        <strong>{item.title || "The agent has a question"}</strong>
      </div>
      {item.message && <div className="muted">{item.message}</div>}
      {body}
      <div className="row end">
        <button
          className="btn btn-small"
          disabled={sending}
          onClick={() => void send({ ...base, cancelled: true })}
        >
          Dismiss
        </button>
      </div>
    </div>
  );
}
