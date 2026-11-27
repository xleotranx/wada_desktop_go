import { describe, expect, it } from "vitest";
import { TerminalTextStream, appendTerminalOutput } from "./ansi";

const enc = (s: string) => new TextEncoder().encode(s);

function run(chunks: Uint8Array[]): string {
  const stream = new TerminalTextStream();
  return chunks.map((c) => stream.push(c)).join("");
}

describe("TerminalTextStream", () => {
  it("joins a UTF-8 character split across chunks", () => {
    const bytes = enc("héllo ✓");
    const cut = 2; // inside the two bytes of é
    expect(run([bytes.slice(0, cut), bytes.slice(cut)])).toBe("héllo ✓");
  });

  it("strips a CSI sequence split across chunks", () => {
    expect(run([enc("ok \x1b[0;3"), enc("2mgreen\x1b[0m")])).toBe("ok green");
  });

  it("strips an OSC title split at its ESC \\ terminator", () => {
    expect(run([enc("\x1b]0;title\x1b"), enc("\\prompt")])).toBe("prompt");
  });

  it("keeps a CRLF split across chunks as one newline", () => {
    expect(run([enc("a\r"), enc("\nb")])).toBe("a\nb");
  });

  it("flushes text that only looked like a partial sequence", () => {
    const stream = new TerminalTextStream();
    expect(stream.push(enc("a\x1b["))).toBe("a");
    expect(stream.push(enc("x"))).toBe("");
    expect(stream.push(enc("b\r"))).toBe("b");
    expect(stream.push(enc("c"))).toBe("\nc");
  });
});

describe("appendTerminalOutput", () => {
  it("keeps the most recent output", () => {
    const out = appendTerminalOutput("x".repeat(120_000), "tail");
    expect(out.length).toBe(120_000);
    expect(out.endsWith("tail")).toBe(true);
  });
});
