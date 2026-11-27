export const PROTOCOL_VERSION = 1;

export type FrameOp = 0x01 | 0x02 | 0x03;
export const OP_TERM_OUTPUT = 0x01;
export const OP_TERM_INPUT = 0x02;
export const OP_TERM_RESIZE = 0x03;

export interface Envelope<T = unknown> {
  type: "hello" | "server_info" | "ping" | "pong" | "rpc_request" | "rpc_response" | "event";
  requestId?: string;
  subscriptionId?: string;
  method?: string;
  payload?: T;
  error?: { code: string; message: string };
}

export interface ServerInfo {
  serverId: string;
  hostname?: string;
  version: string;
  protocolVersion: number;
  origin: string;
  features?: string[];
}

export interface SessionInfo {
  id: string;
  title: string;
  directory: string;
  provider?: string;
  model?: { provider: string; id: string };
  updated?: number;
}

export interface ToolInfo {
  id: string;
  name: string;
  kind?: string;
  command?: string;
  status: string;
  output?: string[];
}

export interface MessagePart {
  kind: "text" | "reasoning" | "tool" | "other";
  text?: string;
  tool?: ToolInfo;
}

export interface SessionMessage {
  id: string;
  role: "user" | "assistant" | "other";
  text?: string;
  parts?: MessagePart[];
}

export interface ApprovalItem {
  session: string;
  id: string;
  kind: string;
  action: string;
  resources?: string[];
}

export interface InteractionItem {
  session: string;
  id: string;
  kind: "confirm" | "select" | "input" | "editor";
  title?: string;
  message?: string;
  options?: string[];
  questions?: {
    key: string;
    title: string;
    description?: string;
    options?: { value: string; label: string; description?: string }[];
  }[];
}

export interface DaemonEvent {
  kind: string;
  session?: string;
  epoch?: number;
  seq?: number;
  text?: { message?: string; ordinal?: number; delta?: string; text?: string };
  tool?: ToolInfo;
  approval?: ApprovalItem;
  interaction?: InteractionItem;
  worker?: WorkerInfo;
  terminal?: TerminalInfo;
  error?: string;
  title?: string;
}

export interface HarnessInfo {
  id: string;
  kind: string;
  name: string;
  description?: string;
  binary?: string;
  available: boolean;
}

export interface WorkerInfo {
  id: string;
  parentSession?: string;
  provider: string;
  name: string;
  directory: string;
  branch?: string;
  status: string;
}

export interface FileChange {
  path: string;
  status: "added" | "modified" | "deleted";
  added?: number;
  removed?: number;
  patch?: string;
}

export interface TerminalInfo {
  id: string;
  name: string;
  directory: string;
  shell?: string;
  cols: number;
  rows: number;
  pid?: number;
  running: boolean;
  exitCode?: number;
}

export function encodeBinaryFrame(op: FrameOp, channel: number, payload: Uint8Array): Uint8Array {
  const out = new Uint8Array(2 + payload.length);
  out[0] = op;
  out[1] = channel & 0xff;
  out.set(payload, 2);
  return out;
}

export function decodeBinaryFrame(raw: Uint8Array): {
  op: number;
  channel: number;
  payload: Uint8Array;
} | null {
  if (raw.length < 2) return null;
  return {
    op: raw[0],
    channel: raw[1],
    payload: raw.slice(2),
  };
}

export function encodeTermResize(cols: number, rows: number): Uint8Array {
  const out = new Uint8Array(4);
  const view = new DataView(out.buffer);
  view.setUint16(0, cols, false);
  view.setUint16(2, rows, false);
  return out;
}
