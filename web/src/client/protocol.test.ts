import { describe, expect, it } from "vitest";
import envelopeGo from "../../../internal/protocol/envelope.go?raw";
import messagesGo from "../../../internal/protocol/messages.go?raw";
import * as protocol from "./protocol";
import clientTS from "./wadaClient.ts?raw";

function goStrings(src: string, pattern: RegExp): Set<string> {
  return new Set([...src.matchAll(pattern)].map((m) => m[1]));
}

function exported(prefix: string): string[] {
  return Object.entries(protocol)
    .filter(([name, v]) => name.startsWith(prefix) && typeof v === "string")
    .map(([, v]) => v as string);
}

// The web client spells the daemon's wire names itself; these tests read the Go
// declarations so the two cannot drift apart unnoticed.
describe("wire names match internal/protocol", () => {
  it("envelope types", () => {
    const goTypes = goStrings(envelopeGo, /^\s*Type\w+\s*=\s*"([^"]+)"/gm);
    expect(goTypes.size).toBeGreaterThan(0);
    expect(new Set(exported("ENV_"))).toEqual(goTypes);
  });

  it("event kinds", () => {
    const goKinds = goStrings(messagesGo, /^\s*Event\w+\s+EventKind\s*=\s*"([^"]+)"/gm);
    expect(goKinds.size).toBeGreaterThan(0);
    for (const kind of exported("EV_")) {
      expect(goKinds).toContain(kind);
    }
  });

  it("RPC methods the client calls", () => {
    const goMethods = goStrings(messagesGo, /^\s*Method\w+\s*=\s*"([^"]+)"/gm);
    const called = goStrings(clientTS, /this\.call(?:<[^>]*>)?\(\s*"([^"]+)"/g);
    expect(called.size).toBeGreaterThan(10);
    for (const m of called) expect(goMethods).toContain(m);
  });

  it("deliveries", () => {
    const goDeliveries = goStrings(messagesGo, /^\s*Deliver\w+\s+Delivery\s*=\s*"([^"]+)"/gm);
    const deliveries: protocol.Delivery[] = ["steer", "queue", "interrupt"];
    expect(goDeliveries).toEqual(new Set(deliveries));
  });
});
