// The service worker's life in the page: register it, and tell the app when a
// new build is waiting so the user picks when to reload.

type Listener = (ready: boolean) => void;

let waiting: ServiceWorker | null = null;
const listeners = new Set<Listener>();

function announce(w: ServiceWorker | null): void {
  waiting = w;
  for (const l of listeners) l(!!w);
}

/** registerServiceWorker registers /sw.js and watches for a waiting build. */
export function registerServiceWorker(): void {
  if (!("serviceWorker" in navigator)) return;
  let reloading = false;
  navigator.serviceWorker.addEventListener("controllerchange", () => {
    if (reloading) return;
    reloading = true;
    window.location.reload();
  });
  window.addEventListener("load", () => {
    navigator.serviceWorker
      .register("/sw.js")
      .then((reg) => {
        // A build that installed while no page took it is waiting already.
        if (reg.waiting && navigator.serviceWorker.controller) announce(reg.waiting);
        reg.addEventListener("updatefound", () => {
          const next = reg.installing;
          next?.addEventListener("statechange", () => {
            // The first install has nothing to replace.
            if (next.state === "installed" && navigator.serviceWorker.controller) announce(next);
          });
        });
        // Long-lived installed apps look for a new build now and then.
        window.setInterval(() => void reg.update().catch(() => {}), 60 * 60 * 1000);
        document.addEventListener("visibilitychange", () => {
          if (document.visibilityState === "visible") void reg.update().catch(() => {});
        });
      })
      .catch(() => {});
  });
}

/** onUpdateReady calls l with whether a new build waits; it returns the unsubscribe. */
export function onUpdateReady(l: Listener): () => void {
  listeners.add(l);
  l(!!waiting);
  return () => listeners.delete(l);
}

/** applyUpdate activates the waiting build; the page reloads when it takes over. */
export function applyUpdate(): void {
  waiting?.postMessage("skip-waiting");
}
