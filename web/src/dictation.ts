// Dictation with the browser's speech recognition, where it has one
// (Chrome, Edge and Safari do; Firefox does not).

interface Recognition {
  continuous: boolean;
  interimResults: boolean;
  onresult: ((e: { resultIndex: number; results: ArrayLike<{ isFinal: boolean } & ArrayLike<{ transcript: string }>> }) => void) | null;
  onerror: ((e: { error?: string }) => void) | null;
  onend: (() => void) | null;
  start(): void;
  stop(): void;
  abort(): void;
}

type RecognitionCtor = new () => Recognition;

function ctor(): RecognitionCtor | undefined {
  const w = globalThis as unknown as { SpeechRecognition?: RecognitionCtor; webkitSpeechRecognition?: RecognitionCtor };
  return w.SpeechRecognition || w.webkitSpeechRecognition;
}

export function dictationSupported(): boolean {
  return !!ctor();
}

/**
 * startDictation listens until stopped: onText gets everything heard so far
 * each time it changes, and onEnd the error that ended it, if any. It returns
 * the stop function.
 */
export function startDictation(onText: (text: string) => void, onEnd: (error?: string) => void): () => void {
  const R = ctor();
  if (!R) {
    onEnd("Speech recognition isn't available in this browser.");
    return () => {};
  }
  const rec = new R();
  rec.continuous = true;
  rec.interimResults = true;
  let done = "";
  let error = "";
  rec.onresult = (e) => {
    let now = "";
    for (let i = e.resultIndex; i < e.results.length; i++) {
      const r = e.results[i];
      if (r.isFinal) done += r[0].transcript;
      else now += r[0].transcript;
    }
    onText((done + now).trim());
  };
  rec.onerror = (e) => {
    error = e.error || "error";
  };
  rec.onend = () => onEnd(error && error !== "aborted" && error !== "no-speech" ? problem(error) : undefined);
  try {
    rec.start();
  } catch (err) {
    onEnd(err instanceof Error ? err.message : String(err));
  }
  return () => rec.stop();
}

/** joinDictation puts dictated text after the draft, with a space between. */
export function joinDictation(base: string, heard: string): string {
  if (!heard) return base;
  if (!base || base.endsWith(" ") || base.endsWith("\n")) return base + heard;
  return base + " " + heard;
}

function problem(code: string): string {
  switch (code) {
    case "not-allowed":
    case "service-not-allowed":
      return "Dictation needs the microphone; allow it for this site.";
    case "audio-capture":
      return "No microphone could be used.";
    case "network":
      return "Speech recognition needs the network.";
  }
  return "Speech recognition failed: " + code;
}
