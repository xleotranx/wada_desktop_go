import { describe, expect, it } from "vitest";
import vector from "../../../internal/relay/testdata/noise_nk_vector.json";
import {
  NoiseNKInitiator,
  NoiseNKResponder,
  bytesToHex,
  hexToBytes,
  utf8Bytes,
} from "./noise";
import { decodeOffer, encodeBase64Url, encodeOffer, relayClientWSURL } from "./offer";

describe("Noise_NK_25519_ChaChaPoly_SHA256 interoperability with Go", () => {
  it("matches Go golden test vector byte-for-byte", () => {
    const prologue = hexToBytes(vector.prologueHex);
    const rsPriv = hexToBytes(vector.rsPrivHex);
    const rsPub = hexToBytes(vector.rsPubHex);
    const eiPriv = hexToBytes(vector.eiPrivHex);
    const erPriv = hexToBytes(vector.erPrivHex);

    const initiator = new NoiseNKInitiator(rsPub, prologue, eiPriv);
    const responder = new NoiseNKResponder(rsPriv, prologue, erPriv);

    const msg1 = initiator.writeMessage(hexToBytes(vector.msg1PlainHex));
    expect(bytesToHex(msg1)).toBe(vector.msg1WireHex);

    const read1 = responder.readMessage(msg1);
    expect(bytesToHex(read1)).toBe(vector.msg1PlainHex);

    const { msg: msg2, session: respSession } = responder.writeMessage(
      hexToBytes(vector.msg2PlainHex)
    );
    expect(bytesToHex(msg2)).toBe(vector.msg2WireHex);

    const { payload: read2, session: initSession } = initiator.readMessage(msg2);
    expect(bytesToHex(read2)).toBe(vector.msg2PlainHex);
    expect(bytesToHex(initSession.handshakeHash)).toBe(vector.handshakeHashHex);
    expect(bytesToHex(respSession.handshakeHash)).toBe(vector.handshakeHashHex);

    expect(bytesToHex(initSession.send.key!)).toBe(vector.initSendKeyHex);
    expect(bytesToHex(initSession.recv.key!)).toBe(vector.initRecvKeyHex);

    const appCT = initSession.send.encryptWithAD(new Uint8Array(0), hexToBytes(vector.appPlainHex));
    expect(bytesToHex(appCT)).toBe(vector.appCipherHex);

    const appPT = respSession.recv.decryptWithAD(new Uint8Array(0), appCT);
    expect(bytesToHex(appPT)).toBe(vector.appPlainHex);
  });

  it("rejects wrong keys, tampered ciphertexts, and replayed frames", () => {
    const rsPriv = hexToBytes(vector.rsPrivHex);
    const rsPub = hexToBytes(vector.rsPubHex);
    const wrongPub = new Uint8Array(rsPub);
    wrongPub[0] ^= 0x01;

    const badInit = new NoiseNKInitiator(wrongPub);
    const resp = new NoiseNKResponder(rsPriv);
    const badMsg1 = badInit.writeMessage(utf8Bytes("hi"));
    expect(() => resp.readMessage(badMsg1)).toThrow();

    const init = new NoiseNKInitiator(rsPub);
    const resp2 = new NoiseNKResponder(rsPriv);
    const msg1 = init.writeMessage(utf8Bytes("ok"));
    resp2.readMessage(msg1);
    const { msg: msg2, session: rSess } = resp2.writeMessage(utf8Bytes("ack"));
    const { session: iSess } = init.readMessage(msg2);

    const frame0 = iSess.send.encryptWithAD(new Uint8Array(0), utf8Bytes("secret-rpc"));
    const tampered = new Uint8Array(frame0);
    tampered[tampered.length - 1] ^= 0xff;
    expect(() => rSess.recv.decryptWithAD(new Uint8Array(0), tampered)).toThrow();

    // Valid frame still succeeds without nonce desync, then replay fails.
    expect(new TextDecoder().decode(rSess.recv.decryptWithAD(new Uint8Array(0), frame0))).toBe(
      "secret-rpc"
    );
    expect(() => rSess.recv.decryptWithAD(new Uint8Array(0), frame0)).toThrow();
  });

  it("decodes #offer= links and builds relay URLs", () => {
    const pubB64 = encodeBase64Url(hexToBytes(vector.rsPubHex));
    const encoded = encodeOffer({
      v: 1,
      relayUrl: "https://relay.wada.dev",
      serverId: "srv_1",
      serverPubKey: pubB64,
      token: "tok_123",
    });
    const fullUrl = `https://app.wada.dev/#offer=${encoded}`;
    const decoded = decodeOffer(fullUrl);
    expect(decoded.serverId).toBe("srv_1");
    expect(decoded.token).toBe("tok_123");

    const wsUrl = relayClientWSURL(decoded.relayUrl!, hexToBytes(vector.rsPubHex), "relay_tok");
    expect(wsUrl).toMatch(/^wss:\/\/relay\.wada\.dev\/ws\/client\/[0-9a-f]{32}\?token=relay_tok$/);
  });
});
