import {
  NoiseNKInitiator,
  type NoiseSession,
  concatBytes,
  utf8Bytes,
} from "../crypto/noise";
import {
  decodeBase64Url,
  decodeOffer,
  normalizeDirectWSURL,
  relayClientWSURL,
} from "../crypto/offer";
import {
  type AgentProfile,
  type DaemonEvent,
  type Delivery,
  ENV_EVENT,
  ENV_HELLO,
  ENV_PING,
  ENV_PONG,
  ENV_RPC_REQUEST,
  ENV_RPC_RESPONSE,
  ENV_SERVER_INFO,
  type Envelope,
  type FileChange,
  type HarnessInfo,
  type InteractionItem,
  OP_TERM_INPUT,
  type PendingApproval,
  OP_TERM_OUTPUT,
  PROTOCOL_VERSION,
  type Schedule,
  type ScheduleSpec,
  type ServerInfo,
  type ProjectInfo,
  type SessionInfo,
  type WorkspaceConfig,
  type WorkspaceCreate,
  type WorkspaceInfo,
  type SessionMessage,
  type TerminalInfo,
  type WorkerInfo,
  decodeBinaryFrame,
  encodeBinaryFrame,
} from "./protocol";

const WS_TEXT = 1;
const WS_BINARY = 2;
// The client pings this often and treats a ping unanswered by the next one
// as a dead connection.
const PING_INTERVAL_MS = 10_000;

interface ConnectConfig {
  offer?: string;
  endpoint?: string;
  credential?: string;
  password?: string;
  relayAuthToken?: string;
}

export interface AttachedTerminal {
  writeInput: (data: Uint8Array) => void;
  close: () => Promise<void>;
}

export class WadaClient {
  private ws: WebSocket | null = null;
  private noise: NoiseSession | null = null;
  private pending = new Map<
    string,
    { resolve: (val: unknown) => void; reject: (err: Error) => void; timer: number }
  >();
  private eventListeners = new Set<(ev: DaemonEvent) => void>();
  private statusListeners = new Set<(state: "connected" | "disconnected", err?: string) => void>();
  private termListeners = new Map<number, (chunk: Uint8Array) => void>();
  private pingTimer: number | null = null;
  private probeTimer: number | null = null;
  private closed = false;
  // live is true from a completed connect until the socket is torn down.
  private live = false;
  private subId = "";
  public mode: "relay" | "direct" = "direct";

  constructor(private readonly cfg: ConnectConfig) {
    if (typeof document !== "undefined") {
      document.addEventListener("visibilitychange", this.handleVisibility);
    }
  }

  onEvent(fn: (ev: DaemonEvent) => void): () => void {
    this.eventListeners.add(fn);
    return () => this.eventListeners.delete(fn);
  }

  onStatus(fn: (state: "connected" | "disconnected", err?: string) => void): () => void {
    this.statusListeners.add(fn);
    return () => this.statusListeners.delete(fn);
  }

  async connect(): Promise<ServerInfo> {
    this.closed = false;
    let cred = this.cfg.credential || "";
    const password = this.cfg.password || "";

    let wsURL = "";
    let serverPub: Uint8Array | null = null;

    if (this.cfg.offer) {
      const off = decodeOffer(this.cfg.offer);
      if (!cred && !password && off.token) cred = off.token;
      if (off.relayUrl) {
        serverPub = decodeBase64Url(off.serverPubKey);
        wsURL = relayClientWSURL(off.relayUrl, serverPub, this.cfg.relayAuthToken);
        this.mode = "relay";
      } else if (off.directUrl) {
        wsURL = normalizeDirectWSURL(off.directUrl);
        this.mode = "direct";
      }
    } else if (this.cfg.endpoint) {
      wsURL = normalizeDirectWSURL(this.cfg.endpoint);
      this.mode = "direct";
    } else {
      throw new Error("No offer or endpoint provided");
    }

    const ws = new WebSocket(wsURL);
    ws.binaryType = "arraybuffer";
    this.ws = ws;
    try {
      return await this.handshake(ws, serverPub, cred, password);
    } catch (err) {
      // A failed connect reports through its rejection, not the status listeners.
      this.teardown(err instanceof Error ? err.message : "Connection failed");
      throw err;
    }
  }

  private async handshake(
    ws: WebSocket,
    serverPub: Uint8Array | null,
    cred: string,
    password: string
  ): Promise<ServerInfo> {
    await new Promise<void>((resolve, reject) => {
      const onOpen = () => {
        ws.removeEventListener("error", onErr);
        resolve();
      };
      const onErr = () => {
        ws.removeEventListener("open", onOpen);
        reject(new Error("WebSocket connection failed"));
      };
      ws.addEventListener("open", onOpen, { once: true });
      ws.addEventListener("error", onErr, { once: true });
    });

    if (serverPub) {
      const hs = new NoiseNKInitiator(serverPub);
      const msg1 = hs.writeMessage();
      ws.send(msg1);
      const msg2 = await this.readNextBinary(ws);
      const { session } = hs.readMessage(msg2);
      this.noise = session;
    } else {
      this.noise = null;
    }

    const helloId = crypto.randomUUID();
    const helloEnv: Envelope = {
      type: ENV_HELLO,
      requestId: helloId,
      payload: {
        clientId: "web_" + crypto.randomUUID(),
        clientType: "web",
        protocolVersion: PROTOCOL_VERSION,
        appVersion: "0.1.0-dev",
        credential: cred || undefined,
        password: password || undefined,
        capabilities: ["owned_subscriptions", "creation_receipts"],
      },
    };
    this.sendEnvelope(helloEnv);

    const infoEnv = await this.readNextEnvelope(ws);
    if (infoEnv.error) {
      throw new Error(`${infoEnv.error.code}: ${infoEnv.error.message}`);
    }
    if (infoEnv.type !== ENV_SERVER_INFO || !infoEnv.payload) {
      throw new Error(`Unexpected handshake frame: ${infoEnv.type}`);
    }
    const serverInfo = infoEnv.payload as ServerInfo;

    // Only the current socket may drive the client: a socket that was torn
    // down can still deliver frames or a late close.
    ws.addEventListener("message", (ev) => {
      if (this.ws === ws) this.handleMessage(ev.data);
    });
    ws.addEventListener("close", () => {
      if (this.ws === ws) this.handleClose("Connection closed");
    });
    ws.addEventListener("error", () => {
      if (this.ws === ws) this.handleClose("WebSocket error");
    });

    this.startPing();
    const sub = await this.call<{ subscriptionId: string }>("events.subscribe", {});
    this.subId = sub.subscriptionId;
    this.live = true;

    for (const cb of this.statusListeners) cb("connected");
    return serverInfo;
  }

  close(): void {
    this.closed = true;
    if (typeof document !== "undefined") {
      document.removeEventListener("visibilitychange", this.handleVisibility);
    }
    this.teardown("Client closed");
  }

  // teardown closes the current socket and fails its pending calls. It reports
  // whether there was a socket to tear down.
  private teardown(reason: string): boolean {
    this.cleanupTimers();
    this.live = false;
    const ws = this.ws;
    this.ws = null;
    this.noise = null;
    this.termListeners.clear();
    for (const [id, p] of this.pending) {
      window.clearTimeout(p.timer);
      p.reject(new Error(reason));
      this.pending.delete(id);
    }
    if (!ws) return false;
    try {
      ws.close();
    } catch {
      // The socket is being discarded either way.
    }
    return true;
  }

  private cleanupTimers(): void {
    if (this.pingTimer !== null) {
      window.clearInterval(this.pingTimer);
      this.pingTimer = null;
    }
    if (this.probeTimer !== null) {
      window.clearTimeout(this.probeTimer);
      this.probeTimer = null;
    }
  }

  private startPing(): void {
    this.cleanupTimers();
    this.pingTimer = window.setInterval(() => {
      // A pong clears the probe; one still armed from the last ping means
      // the socket has been silent for a whole interval and is reported dead.
      if (this.probeTimer !== null) return;
      this.sendPing();
      this.armProbe(PING_INTERVAL_MS, "Liveness check timed out");
    }, PING_INTERVAL_MS);
  }

  private armProbe(ms: number, reason: string): void {
    if (this.probeTimer !== null) window.clearTimeout(this.probeTimer);
    this.probeTimer = window.setTimeout(() => {
      this.probeTimer = null;
      if (!this.closed) {
        this.handleClose(reason);
      }
    }, ms);
  }

  private sendPing(): void {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return;
    this.sendEnvelope({
      type: ENV_PING,
      requestId: crypto.randomUUID(),
      payload: { timestamp: Date.now() },
    });
  }

  private handleVisibility = (): void => {
    if (document.visibilityState !== "visible" || this.closed || !this.live) return;
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      this.handleClose("Socket closed while backgrounded");
      return;
    }
    this.sendPing();
    this.armProbe(5_000, "Foreground liveness check timed out");
  };

  private handleClose(reason: string): void {
    if (!this.teardown(reason)) return;
    if (!this.closed) {
      for (const cb of this.statusListeners) cb("disconnected", reason);
    }
  }

  private readNextBinary(ws: WebSocket): Promise<Uint8Array> {
    return new Promise((resolve, reject) => {
      const cleanup = () => {
        ws.removeEventListener("message", onMsg);
        ws.removeEventListener("close", onClose);
      };
      const onMsg = (ev: MessageEvent) => {
        if (ev.data instanceof ArrayBuffer) {
          cleanup();
          resolve(new Uint8Array(ev.data));
        }
      };
      const onClose = () => {
        cleanup();
        reject(new Error("Closed during handshake"));
      };
      ws.addEventListener("message", onMsg);
      ws.addEventListener("close", onClose);
    });
  }

  private readNextEnvelope(ws: WebSocket): Promise<Envelope> {
    return new Promise((resolve, reject) => {
      const cleanup = () => {
        ws.removeEventListener("message", onMsg);
        ws.removeEventListener("close", onClose);
      };
      const onMsg = (ev: MessageEvent) => {
        cleanup();
        try {
          const decoded = this.decodeWireMessage(ev.data);
          if (!decoded || decoded.kind !== WS_TEXT) {
            reject(new Error("Expected JSON handshake frame"));
            return;
          }
          resolve(JSON.parse(new TextDecoder().decode(decoded.payload)) as Envelope);
        } catch (err) {
          reject(err instanceof Error ? err : new Error(String(err)));
        }
      };
      const onClose = () => {
        cleanup();
        reject(new Error("Closed during server_info"));
      };
      ws.addEventListener("message", onMsg);
      ws.addEventListener("close", onClose);
    });
  }

  private decodeWireMessage(
    data: unknown
  ): { kind: typeof WS_TEXT | typeof WS_BINARY; payload: Uint8Array } | null {
    if (this.noise) {
      if (!(data instanceof ArrayBuffer)) return null;
      const pt = this.noise.recv.decryptWithAD(new Uint8Array(0), new Uint8Array(data));
      if (pt.length === 0) return null;
      const kind = pt[0];
      if (kind !== WS_TEXT && kind !== WS_BINARY) return null;
      return { kind, payload: pt.slice(1) };
    }
    if (typeof data === "string") {
      return { kind: WS_TEXT, payload: utf8Bytes(data) };
    }
    if (data instanceof ArrayBuffer) {
      return { kind: WS_BINARY, payload: new Uint8Array(data) };
    }
    return null;
  }

  private sendWire(kind: typeof WS_TEXT | typeof WS_BINARY, payload: Uint8Array): void {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      throw new Error("Not connected to daemon");
    }
    if (this.noise) {
      const ct = this.noise.send.encryptWithAD(
        new Uint8Array(0),
        concatBytes(Uint8Array.of(kind), payload)
      );
      this.ws.send(ct);
      return;
    }
    if (kind === WS_TEXT) {
      this.ws.send(new TextDecoder().decode(payload));
    } else {
      this.ws.send(payload);
    }
  }

  private sendEnvelope(env: Envelope): void {
    this.sendWire(WS_TEXT, utf8Bytes(JSON.stringify(env)));
  }

  private handleMessage(rawData: unknown): void {
    let decoded: { kind: typeof WS_TEXT | typeof WS_BINARY; payload: Uint8Array } | null = null;
    try {
      decoded = this.decodeWireMessage(rawData);
    } catch (err) {
      this.handleClose(err instanceof Error ? err.message : "Decryption failed");
      return;
    }
    if (!decoded) return;

    if (decoded.kind === WS_BINARY) {
      const frame = decodeBinaryFrame(decoded.payload);
      if (frame && frame.op === OP_TERM_OUTPUT) {
        const listener = this.termListeners.get(frame.channel);
        if (listener) listener(frame.payload);
      }
      return;
    }

    const env = JSON.parse(new TextDecoder().decode(decoded.payload)) as Envelope;
    if (env.type === ENV_PONG) {
      if (this.probeTimer !== null) {
        window.clearTimeout(this.probeTimer);
        this.probeTimer = null;
      }
      return;
    }
    if (env.type === ENV_RPC_RESPONSE && env.requestId) {
      const entry = this.pending.get(env.requestId);
      if (entry) {
        window.clearTimeout(entry.timer);
        this.pending.delete(env.requestId);
        if (env.error) entry.reject(new Error(`${env.error.code}: ${env.error.message}`));
        else entry.resolve(env.payload);
      }
      return;
    }
    if (env.type === ENV_EVENT && env.payload) {
      const ev = env.payload as DaemonEvent;
      for (const cb of this.eventListeners) cb(ev);
    }
  }

  async call<T = unknown>(method: string, payload?: unknown): Promise<T> {
    const requestId = crypto.randomUUID();
    return new Promise<T>((resolve, reject) => {
      const timer = window.setTimeout(() => {
        this.pending.delete(requestId);
        reject(new Error(`RPC ${method} timed out`));
      }, 60_000);
      this.pending.set(requestId, {
        resolve: resolve as (v: unknown) => void,
        reject,
        timer,
      });
      try {
        this.sendEnvelope({ type: ENV_RPC_REQUEST, requestId, method, payload });
      } catch (err) {
        window.clearTimeout(timer);
        this.pending.delete(requestId);
        reject(err instanceof Error ? err : new Error(String(err)));
      }
    });
  }

  listSessions(): Promise<{ sessions: SessionInfo[] }> {
    return this.call("agent.sessions.list", {});
  }

  listProjects(): Promise<ProjectInfo[]> {
    return this.call("workspace.projects.list", {});
  }

  listWorkspaces(): Promise<WorkspaceInfo[]> {
    return this.call("workspace.list", {});
  }

  createSession(
    directory: string,
    provider?: string,
    mode = "ask",
    opts: { workspace?: string; profile?: string } = {}
  ): Promise<SessionInfo> {
    return this.call("agent.session.create", {
      idempotencyKey: "web_" + crypto.randomUUID(),
      directory: opts.workspace ? undefined : directory,
      workspace: opts.workspace || undefined,
      provider: provider || undefined,
      profile: opts.profile || undefined,
      mode,
    });
  }

  createWorkspace(req: WorkspaceCreate): Promise<WorkspaceInfo> {
    return this.call("workspace.create", { idempotencyKey: "web_" + crypto.randomUUID(), ...req });
  }

  /** Archives a workspace; without force, one with unsaved work is refused (conflict). */
  archiveWorkspace(workspace: string, force = false): Promise<void> {
    return this.call("workspace.archive", { workspace, force: force || undefined });
  }

  workspaceConfig(workspace: string): Promise<WorkspaceConfig> {
    return this.call("workspace.config", { workspace });
  }

  trustWorkspaceConfig(workspace: string, hash: string): Promise<void> {
    return this.call("workspace.trust", { workspace, hash });
  }

  runSetup(workspace: string): Promise<TerminalInfo> {
    return this.call("workspace.setup", { workspace });
  }

  startService(workspace: string, name: string): Promise<TerminalInfo> {
    return this.call("workspace.service.start", { workspace, name });
  }

  stopService(workspace: string, name: string): Promise<void> {
    return this.call("workspace.service.stop", { workspace, name });
  }

  listSchedules(): Promise<Schedule[]> {
    return this.call("schedules.list", {});
  }

  createSchedule(spec: ScheduleSpec): Promise<Schedule> {
    return this.call("schedules.create", spec);
  }

  updateSchedule(id: string, spec: ScheduleSpec): Promise<Schedule> {
    return this.call("schedules.update", { id, spec });
  }

  deleteSchedule(id: string): Promise<void> {
    return this.call("schedules.delete", { id });
  }

  /** Runs a schedule now, without changing its next time. */
  runSchedule(id: string): Promise<void> {
    return this.call("schedules.run", { id });
  }

  listProfiles(directory: string): Promise<AgentProfile[]> {
    return this.call("agent.profiles.list", { directory: directory || undefined });
  }

  getMessages(session: string): Promise<SessionMessage[]> {
    return this.call("agent.session.messages", { session });
  }

  /** Sends a message; deliver says what happens to one sent while a turn runs. */
  sendMessage(session: string, text: string, mode = "ask", deliver?: Delivery): Promise<{ disposition: string }> {
    const id = "web_" + crypto.randomUUID();
    return this.call("agent.session.send", {
      id,
      messageId: id,
      session,
      mode,
      text,
      deliver,
    });
  }

  abortSession(session: string): Promise<void> {
    return this.call("agent.session.abort", { session });
  }

  replyApproval(session: string, approval: string, decision: "once" | "always" | "reject"): Promise<void> {
    return this.call("agent.session.reply", { session, approval, decision });
  }

  answerInteraction(ans: {
    session: string;
    id: string;
    confirmed?: boolean;
    value?: string;
    cancelled?: boolean;
    answers?: Record<string, string[]>;
  }): Promise<void> {
    return this.call("agent.interactions.answer", ans);
  }

  pendingApprovals(): Promise<PendingApproval[] | null> {
    return this.call("agent.approvals.pending", {});
  }

  pendingInteractions(session: string): Promise<InteractionItem[] | null> {
    return this.call("agent.interactions.pending", { session });
  }

  getChanges(session: string): Promise<FileChange[]> {
    return this.call("agent.session.changes", { session });
  }

  listHarnesses(): Promise<HarnessInfo[]> {
    return this.call("agent.harnesses.list");
  }

  listWorkers(parentSession?: string): Promise<WorkerInfo[]> {
    return this.call("agent.workers.list", { parentSession: parentSession || "" });
  }

  listTerminals(): Promise<TerminalInfo[]> {
    return this.call("terminal.list");
  }

  createTerminal(directory: string, cols = 80, rows = 24): Promise<TerminalInfo> {
    return this.call("terminal.create", { directory, cols, rows });
  }

  killTerminal(id: string): Promise<void> {
    return this.call("terminal.kill", { id });
  }

  // attachTerminal delivers the terminal's snapshot through onChunk first, then
  // its live output.
  async attachTerminal(
    id: string,
    onChunk: (data: Uint8Array) => void,
    cols = 80,
    rows = 24
  ): Promise<AttachedTerminal> {
    const res = await this.call<{ channel: number; info: TerminalInfo; snapshot?: string }>(
      "terminal.attach",
      { id, cols, rows }
    );
    if (res.snapshot) onChunk(decodeBase64Url(res.snapshot));
    this.termListeners.set(res.channel, onChunk);
    return {
      writeInput: (data: Uint8Array) => {
        this.sendWire(WS_BINARY, encodeBinaryFrame(OP_TERM_INPUT, res.channel, data));
      },
      close: async () => {
        this.termListeners.delete(res.channel);
        try {
          await this.call("terminal.detach", { id });
        } catch {
          // ignore detach failure on disconnect
        }
      },
    };
  }

  get subID(): string {
    return this.subId;
  }
}
