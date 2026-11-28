import { describe, expect, it } from "vitest";
import { encodeBase64Url, encodeOffer, parseHostTarget } from "./offer";

describe("parseHostTarget", () => {
  it("adds the daemon port to a bare host name", () => {
    expect(parseHostTarget("my-mac.tailnet.ts.net")).toEqual({ endpoint: "my-mac.tailnet.ts.net:6767" });
    expect(parseHostTarget("100.64.0.4")).toEqual({ endpoint: "100.64.0.4:6767" });
    expect(parseHostTarget("fd7a::1")).toEqual({ endpoint: "[fd7a::1]:6767" });
    expect(parseHostTarget("[fd7a::1]")).toEqual({ endpoint: "[fd7a::1]:6767" });
  });

  it("keeps an explicit port or URL", () => {
    expect(parseHostTarget("100.64.0.4:7000")).toEqual({ endpoint: "100.64.0.4:7000" });
    expect(parseHostTarget("[fd7a::1]:7000")).toEqual({ endpoint: "[fd7a::1]:7000" });
    expect(parseHostTarget("ws://box:6767/ws")).toEqual({ endpoint: "ws://box:6767/ws" });
  });

  it("recognizes offers", () => {
    const offer = encodeOffer({
      v: 1,
      directUrl: "ws://box:6767/ws",
      serverId: "srv",
      serverPubKey: encodeBase64Url(new Uint8Array(32).fill(7)),
    });
    expect(parseHostTarget(offer)).toEqual({ offer });
    expect(parseHostTarget(`https://app.wada.dev/#offer=${offer}`)).toEqual({
      offer: `https://app.wada.dev/#offer=${offer}`,
    });
  });
});
