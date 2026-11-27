import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ENV_EVENT, ENV_RPC_REQUEST, ENV_RPC_RESPONSE, ENV_SERVER_INFO, type Envelope } from "./protocol";
import { WadaClient } from "./wadaClient";

type Reply = (req: Envelope) => Envelope | undefined;

// FakeSocket is a direct (unencrypted) daemon connection that answers hello
// with server_info and every rpc.request through reply.
class FakeSocket extends EventTarget {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSED = 3;
  static last: FakeSocket;
  static reply: Reply = () => ({ type: ENV_RPC_RESPONSE, payload: {} });

  readyState = FakeSocket.CONNECTING;
  binaryType = "blob";
  closeCalls = 0;
  requests: Envelope[] = [];

  constructor(readonly url: string) {
    super();
    FakeSocket.last = this;
    queueMicrotask(() => {
      this.readyState = FakeSocket.OPEN;
      this.dispatchEvent(new Event("open"));
    });
  }

  send(data: unknown): void {
    if (typeof data !== "string") return;
    const env = JSON.parse(data) as Envelope;
    if (env.type === "hello") {
      this.deliverLater({ type: ENV_SERVER_INFO, requestId: env.requestId, payload: { serverId: "srv" } });
      return;
    }
    if (env.type !== ENV_RPC_REQUEST) return;
    this.requests.push(env);
    const resp = FakeSocket.reply(env);
    if (resp) this.deliverLater({ ...resp, requestId: env.requestId });
  }

  close(): void {
    this.closeCalls++;
    if (this.readyState === FakeSocket.CLOSED) return;
    this.readyState = FakeSocket.CLOSED;
    this.dispatchEvent(new Event("close"));
  }

  deliver(env: Envelope): void {
    this.dispatchEvent(new MessageEvent("message", { data: JSON.stringify(env) }));
  }

  deliverBinary(bytes: number[]): void {
    this.dispatchEvent(new MessageEvent("message", { data: new Uint8Array(bytes).buffer }));
  }

  private deliverLater(env: Envelope): void {
    setTimeout(() => this.deliver(env), 0);
  }
}

function subscribeOK(req: Envelope): Envelope | undefined {
  if (req.method === "events.subscribe") {
    return { type: ENV_RPC_RESPONSE, payload: { subscriptionId: "sub_1" } };
  }
  return { type: ENV_RPC_RESPONSE, payload: {} };
}

describe("WadaClient", () => {
  beforeEach(() => {
    vi.stubGlobal("window", globalThis);
    vi.stubGlobal("WebSocket", FakeSocket);
    FakeSocket.reply = subscribeOK;
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("speaks rpc.request and subscribes during connect", async () => {
    const cli = new WadaClient({ endpoint: "ws://host:6767" });
    const info = await cli.connect();
    expect(info.serverId).toBe("srv");
    expect(cli.subID).toBe("sub_1");
    expect(FakeSocket.last.requests.map((r) => r.method)).toEqual(["events.subscribe"]);
    cli.close();
  });

  it("closes the socket when connect fails after the handshake", async () => {
    FakeSocket.reply = () => ({ type: ENV_RPC_RESPONSE, error: { code: "internal", message: "boom" } });
    const cli = new WadaClient({ endpoint: "ws://host:6767" });
    const statuses: string[] = [];
    cli.onStatus((st) => statuses.push(st));
    await expect(cli.connect()).rejects.toThrow("internal: boom");
    expect(FakeSocket.last.closeCalls).toBeGreaterThan(0);
    expect(statuses).toEqual([]);
  });

  it("closes a socket it gives up on and ignores what it delivers later", async () => {
    const cli = new WadaClient({ endpoint: "ws://host:6767" });
    await cli.connect();
    const ws = FakeSocket.last;
    const statuses: string[] = [];
    const events: unknown[] = [];
    cli.onStatus((st) => statuses.push(st));
    cli.onEvent((ev) => events.push(ev));

    ws.dispatchEvent(new Event("error"));
    expect(ws.closeCalls).toBeGreaterThan(0);
    // The close that follows the error must not report a second disconnect.
    expect(statuses).toEqual(["disconnected"]);

    ws.deliver({ type: ENV_EVENT, payload: { kind: "turn_started", session: "s" } });
    expect(events).toEqual([]);
  });

  it("delivers a terminal's snapshot before its live output", async () => {
    FakeSocket.reply = (req) =>
      req.method === "terminal.attach"
        ? { type: ENV_RPC_RESPONSE, payload: { channel: 3, info: { id: "t1" }, snapshot: btoa("snap") } }
        : subscribeOK(req);
    const cli = new WadaClient({ endpoint: "ws://host:6767" });
    await cli.connect();
    const chunks: string[] = [];
    await cli.attachTerminal("t1", (c) => chunks.push(new TextDecoder().decode(c)));
    FakeSocket.last.deliverBinary([0x01, 3, ..."live".split("").map((ch) => ch.charCodeAt(0))]);
    expect(chunks).toEqual(["snap", "live"]);
    cli.close();
  });
});
