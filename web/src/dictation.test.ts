import { afterEach, describe, expect, it } from "vitest";
import { dictationSupported, joinDictation, startDictation } from "./dictation";

class FakeRecognition {
  static last: FakeRecognition;
  continuous = false;
  interimResults = false;
  onresult: ((e: unknown) => void) | null = null;
  onerror: ((e: { error?: string }) => void) | null = null;
  onend: (() => void) | null = null;
  started = false;
  constructor() {
    FakeRecognition.last = this;
  }
  start() {
    this.started = true;
  }
  stop() {
    this.onend?.();
  }
  abort() {}
}

function result(index: number, list: [string, boolean][]) {
  return {
    resultIndex: index,
    results: list.map(([t, f]) => Object.assign([{ transcript: t }], { isFinal: f })),
  };
}

afterEach(() => {
  delete (globalThis as Record<string, unknown>).webkitSpeechRecognition;
});

describe("dictation", () => {
  it("is missing without a recognizer", () => {
    expect(dictationSupported()).toBe(false);
    let ended = "";
    startDictation(() => {}, (e) => (ended = e || ""));
    expect(ended).toContain("isn't available");
  });

  it("reports what it hears and why it ended", () => {
    (globalThis as Record<string, unknown>).webkitSpeechRecognition = FakeRecognition;
    expect(dictationSupported()).toBe(true);
    const heard: string[] = [];
    let ended: string | undefined = "not yet";
    const stop = startDictation((t) => heard.push(t), (e) => (ended = e));
    const rec = FakeRecognition.last;
    expect(rec.started && rec.continuous && rec.interimResults).toBe(true);
    rec.onresult?.(result(0, [["fix the", false]]));
    rec.onresult?.(result(0, [["fix the ", true], ["login", false]]));
    expect(heard).toEqual(["fix the", "fix the login"]);
    rec.onerror?.({ error: "not-allowed" });
    stop();
    expect(ended).toContain("microphone");
  });

  it("joins dictation to the draft", () => {
    expect(joinDictation("", "hi")).toBe("hi");
    expect(joinDictation("a", "b")).toBe("a b");
    expect(joinDictation("a ", "b")).toBe("a b");
    expect(joinDictation("a", "")).toBe("a");
  });
});
