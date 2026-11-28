import { sha256 } from "@noble/hashes/sha2.js";
import { bytesToHex, utf8Bytes } from "./noise";

export interface WadaOffer {
  v: number;
  relayUrl?: string;
  directUrl?: string;
  serverId: string;
  hostname?: string;
  serverPubKey: string;
  token?: string;
  requiresPassword?: boolean;
}

export function decodeBase64Url(input: string): Uint8Array {
  let b64 = input.trim().replace(/-/g, "+").replace(/_/g, "/");
  while (b64.length % 4 !== 0) {
    b64 += "=";
  }
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) {
    out[i] = bin.charCodeAt(i);
  }
  return out;
}

export function encodeBase64Url(bytes: Uint8Array): string {
  let bin = "";
  for (const b of bytes) {
    bin += String.fromCharCode(b);
  }
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

export function serverPubHash(pubKeyBytes: Uint8Array): string {
  const digest = sha256(pubKeyBytes);
  return bytesToHex(digest.slice(0, 16));
}

export function decodeOffer(raw: string): WadaOffer {
  let s = raw.trim();
  if (!s) {
    throw new Error("Empty pairing offer");
  }
  const hashIdx = s.indexOf("#offer=");
  if (hashIdx >= 0) {
    s = s.slice(hashIdx + "#offer=".length);
  }
  const ampIdx = s.indexOf("&");
  if (ampIdx >= 0) {
    s = s.slice(0, ampIdx);
  }
  const decoded = new TextDecoder().decode(decodeBase64Url(s));
  const parsed = JSON.parse(decoded) as WadaOffer;
  if (parsed.v !== 1) {
    throw new Error(`Unsupported offer version: ${parsed.v}`);
  }
  const pub = decodeBase64Url(parsed.serverPubKey);
  if (pub.length !== 32) {
    throw new Error("Invalid server public key length");
  }
  if (!parsed.relayUrl && !parsed.directUrl) {
    throw new Error("Offer missing relayUrl and directUrl");
  }
  return parsed;
}

export function encodeOffer(offer: WadaOffer): string {
  return encodeBase64Url(utf8Bytes(JSON.stringify(offer)));
}

export function relayClientWSURL(baseURL: string, serverPubKey: Uint8Array, authToken?: string): string {
  let raw = baseURL.trim();
  if (!raw.includes("://")) {
    raw = `wss://${raw}`;
  }
  const u = new URL(raw);
  if (u.protocol === "http:") u.protocol = "ws:";
  if (u.protocol === "https:") u.protocol = "wss:";
  const basePath = u.pathname
    .replace(/\/+$/, "")
    .replace(/\/ws\/(client|daemon)$/, "");
  u.pathname = `${basePath}/ws/client/${serverPubHash(serverPubKey)}`;
  if (authToken) {
    u.searchParams.set("token", authToken);
  }
  return u.toString();
}

export const DEFAULT_DAEMON_PORT = 6767;

// parseHostTarget reads what a person typed to pair: a pairing offer (an
// #offer= link or a bare encoded offer) or a daemon endpoint. An endpoint
// without a scheme or port gets the daemon's default port.
export function parseHostTarget(raw: string): { offer: string } | { endpoint: string } {
  const target = raw.trim();
  if (target.includes("#offer=")) return { offer: target };
  try {
    decodeOffer(target);
    return { offer: target };
  } catch {
    // Not an offer: an endpoint.
  }
  if (target.includes("://")) return { endpoint: target };
  const host = target.replace(/\/+$/, "");
  if (/^\[[^\]]*\]:\d+$/.test(host) || /^[^:[\]]+:\d+$/.test(host)) return { endpoint: host };
  if (host.startsWith("[")) return { endpoint: `${host}:${DEFAULT_DAEMON_PORT}` };
  if (host.includes(":")) return { endpoint: `[${host}]:${DEFAULT_DAEMON_PORT}` }; // a bare IPv6 address
  return { endpoint: `${host}:${DEFAULT_DAEMON_PORT}` };
}

export function normalizeDirectWSURL(endpoint: string): string {
  let raw = endpoint.trim();
  if (raw.startsWith("http://")) raw = "ws://" + raw.slice("http://".length);
  else if (raw.startsWith("https://")) raw = "wss://" + raw.slice("https://".length);
  else if (!raw.startsWith("ws://") && !raw.startsWith("wss://")) raw = "ws://" + raw;
  const trimmed = raw.replace(/\/+$/, "");
  return trimmed.endsWith("/ws") ? trimmed : `${trimmed}/ws`;
}
