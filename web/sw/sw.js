// Wada's service worker. The build fills in BUILD and PRECACHE (see
// vite.config.ts): the app shell and its hashed assets, kept per build.
const BUILD = __BUILD__;
const PRECACHE = __PRECACHE__;
const CACHE = "wada-" + BUILD;

self.addEventListener("install", (event) => {
  // A new build waits until the page asks for it, so a running session is
  // never swapped out underneath the user.
  event.waitUntil(caches.open(CACHE).then((cache) => cache.addAll(["/", ...PRECACHE])));
});

self.addEventListener("message", (event) => {
  if (event.data === "skip-waiting") self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) => Promise.all(keys.filter((k) => k.startsWith("wada-") && k !== CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim())
  );
});

self.addEventListener("fetch", (event) => {
  const req = event.request;
  if (req.method !== "GET") return;
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;
  if (url.pathname.startsWith("/api/") || url.pathname.startsWith("/ws")) return;

  if (req.mode === "navigate") {
    // The shell: fresh when online, the cached one when not.
    event.respondWith(
      fetch(req)
        .then((res) => {
          if (res.ok) {
            const copy = res.clone();
            caches.open(CACHE).then((c) => c.put("/", copy));
          }
          return res;
        })
        .catch(() => caches.match("/"))
    );
    return;
  }
  if (url.pathname.startsWith("/assets/")) {
    // Hashed names never change: cached first.
    event.respondWith(
      caches.match(req).then(
        (hit) =>
          hit ||
          fetch(req).then((res) => {
            if (res.ok) {
              const copy = res.clone();
              caches.open(CACHE).then((c) => c.put(req, copy));
            }
            return res;
          })
      )
    );
    return;
  }
  // Icons and the manifest: the cached copy now, refreshed behind it.
  event.respondWith(
    caches.match(req).then((hit) => {
      const fresh = fetch(req)
        .then((res) => {
          if (res.ok) {
            const copy = res.clone();
            caches.open(CACHE).then((c) => c.put(req, copy));
          }
          return res;
        })
        .catch(() => hit);
      return hit || fresh;
    })
  );
});
