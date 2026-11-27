// Lightweight ANSI escape stripper and line accumulator for mobile/web PTY viewing.
const ANSI_REGEX = /\x1b(?:\][^\x07\x1b]*(?:\x07|\x1b\\)|\[[0-9;?]*[A-Za-z]|[()#%][0-9A-Za-z]|.)/g;

export function formatTerminalOutput(prev: string, chunk: Uint8Array): string {
  const text = new TextDecoder().decode(chunk);
  const stripped = text.replace(ANSI_REGEX, "").replace(/\r\n/g, "\n").replace(/\r/g, "\n");
  const next = prev + stripped;
  const maxChars = 120_000;
  return next.length > maxChars ? next.slice(next.length - maxChars) : next;
}
