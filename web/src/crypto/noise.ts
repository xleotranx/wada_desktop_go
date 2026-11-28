import { chacha20poly1305 } from "@noble/ciphers/chacha.js";
import { x25519 } from "@noble/curves/ed25519.js";
import { hmac } from "@noble/hashes/hmac.js";
import { sha256 } from "@noble/hashes/sha2.js";

const NOISE_PROTOCOL_NAME = "Noise_NK_25519_ChaChaPoly_SHA256";
const KEY_SIZE = 32;
const MAX_NONCE = 0xffff_ffff_ffff_ffffn;

export function concatBytes(...parts: Uint8Array[]): Uint8Array {
  let len = 0;
  for (const p of parts) len += p.length;
  const out = new Uint8Array(len);
  let off = 0;
  for (const p of parts) {
    out.set(p, off);
    off += p.length;
  }
  return out;
}

export function utf8Bytes(s: string): Uint8Array {
  return new TextEncoder().encode(s);
}

export function hexToBytes(hex: string): Uint8Array {
  const clean = hex.trim();
  if (clean.length % 2 !== 0) {
    throw new Error("Invalid hex length");
  }
  const out = new Uint8Array(clean.length / 2);
  for (let i = 0; i < out.length; i++) {
    out[i] = Number.parseInt(clean.slice(i * 2, i * 2 + 2), 16);
  }
  return out;
}

export function bytesToHex(bytes: Uint8Array): string {
  let out = "";
  for (const b of bytes) {
    out += b.toString(16).padStart(2, "0");
  }
  return out;
}

function makeNoiseNonce(n: bigint): Uint8Array {
  const nonce = new Uint8Array(12);
  const view = new DataView(nonce.buffer);
  view.setBigUint64(4, n, true);
  return nonce;
}

class CipherState {
  private keyBytes: Uint8Array | null = null;
  private nonce = 0n;

  initializeKey(key: Uint8Array): void {
    if (key.length !== KEY_SIZE) {
      throw new Error("Noise key must be 32 bytes");
    }
    this.keyBytes = new Uint8Array(key);
    this.nonce = 0n;
  }

  get key(): Uint8Array | null {
    return this.keyBytes ? new Uint8Array(this.keyBytes) : null;
  }

  encryptWithAD(ad: Uint8Array, plaintext: Uint8Array): Uint8Array {
    if (!this.keyBytes) {
      return new Uint8Array(plaintext);
    }
    if (this.nonce >= MAX_NONCE) {
      throw new Error("Noise nonce exhausted");
    }
    const nonce = makeNoiseNonce(this.nonce);
    const out = chacha20poly1305(this.keyBytes, nonce, ad).encrypt(plaintext);
    this.nonce += 1n;
    return out;
  }

  decryptWithAD(ad: Uint8Array, ciphertext: Uint8Array): Uint8Array {
    if (!this.keyBytes) {
      return new Uint8Array(ciphertext);
    }
    if (this.nonce >= MAX_NONCE) {
      throw new Error("Noise nonce exhausted");
    }
    const nonce = makeNoiseNonce(this.nonce);
    const out = chacha20poly1305(this.keyBytes, nonce, ad).decrypt(ciphertext);
    this.nonce += 1n;
    return out;
  }
}

function hkdf2(ck: Uint8Array, ikm: Uint8Array): [Uint8Array, Uint8Array] {
  const tempKey = hmac(sha256, ck, ikm);
  const t1 = hmac(sha256, tempKey, Uint8Array.of(0x01));
  const t2 = hmac(sha256, tempKey, concatBytes(t1, Uint8Array.of(0x02)));
  return [t1, t2];
}

class SymmetricState {
  private cs = new CipherState();
  private ck: Uint8Array;
  public h: Uint8Array;

  constructor() {
    const nameBytes = utf8Bytes(NOISE_PROTOCOL_NAME);
    if (nameBytes.length !== KEY_SIZE) {
      throw new Error("Unexpected Noise protocol name length");
    }
    this.h = new Uint8Array(nameBytes);
    this.ck = new Uint8Array(nameBytes);
  }

  mixHash(data: Uint8Array): void {
    this.h = sha256(concatBytes(this.h, data));
  }

  mixKey(ikm: Uint8Array): void {
    const [ck, tempK] = hkdf2(this.ck, ikm);
    this.ck = ck;
    this.cs.initializeKey(tempK);
  }

  encryptAndHash(plaintext: Uint8Array): Uint8Array {
    const ct = this.cs.encryptWithAD(this.h, plaintext);
    this.mixHash(ct);
    return ct;
  }

  decryptAndHash(ciphertext: Uint8Array): Uint8Array {
    const pt = this.cs.decryptWithAD(this.h, ciphertext);
    this.mixHash(ciphertext);
    return pt;
  }

  split(): { c1: CipherState; c2: CipherState; h: Uint8Array } {
    const [k1, k2] = hkdf2(this.ck, new Uint8Array(0));
    const c1 = new CipherState();
    const c2 = new CipherState();
    c1.initializeKey(k1);
    c2.initializeKey(k2);
    return { c1, c2, h: new Uint8Array(this.h) };
  }
}

export interface NoiseSession {
  send: CipherState;
  recv: CipherState;
  handshakeHash: Uint8Array;
}

export class NoiseNKInitiator {
  private ss = new SymmetricState();
  private rs: Uint8Array;
  private ePriv: Uint8Array | null = null;
  private msgIndex = 0;

  constructor(
    responderStaticPub: Uint8Array,
    prologue: Uint8Array = new Uint8Array(0),
    private readonly fixedEphemeralPriv?: Uint8Array
  ) {
    if (responderStaticPub.length !== KEY_SIZE) {
      throw new Error("Responder static public key must be 32 bytes");
    }
    this.rs = new Uint8Array(responderStaticPub);
    this.ss.mixHash(prologue);
    this.ss.mixHash(this.rs);
  }

  writeMessage(payload: Uint8Array = new Uint8Array(0)): Uint8Array {
    if (this.msgIndex !== 0) {
      throw new Error("Unexpected initiator writeMessage state");
    }
    this.ePriv = this.fixedEphemeralPriv
      ? new Uint8Array(this.fixedEphemeralPriv)
      : x25519.utils.randomPrivateKey();
    const ePub = x25519.getPublicKey(this.ePriv);
    this.ss.mixHash(ePub);
    const shared = x25519.getSharedSecret(this.ePriv, this.rs);
    this.ss.mixKey(shared);
    const ct = this.ss.encryptAndHash(payload);
    this.msgIndex = 1;
    return concatBytes(ePub, ct);
  }

  readMessage(msg: Uint8Array): { payload: Uint8Array; session: NoiseSession } {
    if (this.msgIndex !== 1 || !this.ePriv) {
      throw new Error("Unexpected initiator readMessage state");
    }
    if (msg.length < KEY_SIZE) {
      throw new Error("Noise NK response message too short");
    }
    const rePub = msg.slice(0, KEY_SIZE);
    this.ss.mixHash(rePub);
    const shared = x25519.getSharedSecret(this.ePriv, rePub);
    this.ss.mixKey(shared);
    const payload = this.ss.decryptAndHash(msg.slice(KEY_SIZE));
    this.msgIndex = 2;
    const { c1, c2, h } = this.ss.split();
    return {
      payload,
      session: { send: c1, recv: c2, handshakeHash: h },
    };
  }
}

export class NoiseNKResponder {
  private ss = new SymmetricState();
  private sPriv: Uint8Array;
  private rePub: Uint8Array | null = null;
  private msgIndex = 0;

  constructor(
    staticPriv: Uint8Array,
    prologue: Uint8Array = new Uint8Array(0),
    private readonly fixedEphemeralPriv?: Uint8Array
  ) {
    if (staticPriv.length !== KEY_SIZE) {
      throw new Error("Responder static private key must be 32 bytes");
    }
    this.sPriv = new Uint8Array(staticPriv);
    this.ss.mixHash(prologue);
    this.ss.mixHash(x25519.getPublicKey(this.sPriv));
  }

  readMessage(msg: Uint8Array): Uint8Array {
    if (this.msgIndex !== 0) {
      throw new Error("Unexpected responder readMessage state");
    }
    if (msg.length < KEY_SIZE) {
      throw new Error("Noise NK init message too short");
    }
    this.rePub = msg.slice(0, KEY_SIZE);
    this.ss.mixHash(this.rePub);
    const shared = x25519.getSharedSecret(this.sPriv, this.rePub);
    this.ss.mixKey(shared);
    const payload = this.ss.decryptAndHash(msg.slice(KEY_SIZE));
    this.msgIndex = 1;
    return payload;
  }

  writeMessage(payload: Uint8Array = new Uint8Array(0)): {
    msg: Uint8Array;
    session: NoiseSession;
  } {
    if (this.msgIndex !== 1 || !this.rePub) {
      throw new Error("Unexpected responder writeMessage state");
    }
    const ePriv = this.fixedEphemeralPriv
      ? new Uint8Array(this.fixedEphemeralPriv)
      : x25519.utils.randomPrivateKey();
    const ePub = x25519.getPublicKey(ePriv);
    this.ss.mixHash(ePub);
    const shared = x25519.getSharedSecret(ePriv, this.rePub);
    this.ss.mixKey(shared);
    const ct = this.ss.encryptAndHash(payload);
    this.msgIndex = 2;
    const { c1, c2, h } = this.ss.split();
    return {
      msg: concatBytes(ePub, ct),
      session: { send: c2, recv: c1, handshakeHash: h },
    };
  }
}
