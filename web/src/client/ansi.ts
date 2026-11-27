// Lightweight ANSI escape stripper and line accumulator for mobile/web PTY viewing.
const ANSI_REGEX = /\x1b(?:\][^\x07\x1b]*(?:\x07|\x1b\\)|\[[0-9;?]*[A-Za-z]|[()#%][0-9A-Za-z]|.)/g;

// An escape sequence the next chunk may still complete: a bare ESC, an
// unterminated OSC, a CSI without its final byte, or a charset/line prefix.
const PARTIAL_ESCAPE = /^\x1b(?:\][^\x07\x1b]*\x1b?|\[[0-9;?]*|[()#%])?$/;

// A sequence held longer than this is not going to complete; it is flushed so a
// stray ESC cannot swallow the rest of the output.
const MAX_CARRY = 4096;

const MAX_CHARS = 120_000;

// TerminalTextStream turns one terminal's output chunks into plain text. PTY
// reads split UTF-8 characters, escape sequences and CRLF pairs at arbitrary
// byte boundaries, so it keeps the undecoded bytes and an incomplete trailing
// sequence for the next chunk. Use one stream per attachment.
export class TerminalTextStream {
  private decoder = new TextDecoder();
  private carry = "";

  // push returns the plain text that chunk completes.
  push(chunk: Uint8Array): string {
    let text = this.carry + this.decoder.decode(chunk, { stream: true });
    this.carry = "";
    let esc = text.lastIndexOf("\x1b");
    if (esc > 0 && esc === text.length - 1) {
      // A trailing ESC may be the first half of an OSC's ESC \ terminator.
      const prev = text.lastIndexOf("\x1b", esc - 1);
      if (prev >= 0 && PARTIAL_ESCAPE.test(text.slice(prev))) esc = prev;
    }
    if (esc >= 0 && text.length - esc <= MAX_CARRY && PARTIAL_ESCAPE.test(text.slice(esc))) {
      this.carry = text.slice(esc);
      text = text.slice(0, esc);
    } else if (text.endsWith("\r")) {
      // The \n of a CRLF pair may come in the next chunk.
      this.carry = "\r";
      text = text.slice(0, -1);
    }
    return text.replace(ANSI_REGEX, "").replace(/\r\n/g, "\n").replace(/\r/g, "\n");
  }
}

// appendTerminalOutput appends text to prev, keeping the most recent output.
export function appendTerminalOutput(prev: string, text: string): string {
  const next = prev + text;
  return next.length > MAX_CHARS ? next.slice(next.length - MAX_CHARS) : next;
}
