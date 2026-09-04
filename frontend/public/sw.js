// The service worker has exactly one job: when a navigation fails because the
// Go process is not running, show a page that can start it.
//
// This file used to be empty, and empty was the right amount of service
// worker for as long as the binary was the only way in. It is not any more:
// an installed PWA's shortcut only navigates, so clicking it with nothing
// listening lands on the browser's connection error - a dead end with nothing
// in it to press. The cached page below is that missing button.
//
// Everything except a navigation is left strictly alone. Caching the app
// shell would be worse than useless here: a shell served from cache while the
// backend is down looks like a working app and then fails every single call.
// One page is cached, and it is a page that only ever renders when the
// network is genuinely gone.

const CACHE = "nats-desk-offline-v1";
const OFFLINE = "/offline.html";

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches
      .open(CACHE)
      .then((cache) => cache.add(new Request(OFFLINE, { cache: "reload" })))
      .then(() => self.skipWaiting()),
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  );
});

self.addEventListener("fetch", (event) => {
  // Navigations only. An API call gets no interception at all, so a failed
  // one still surfaces as the transport error api.js already reports.
  if (event.request.mode !== "navigate") return;

  event.respondWith(
    // fetch rejects on transport failure and not on a status code, so this
    // catch means "nothing is listening" rather than "the app said no".
    fetch(event.request).catch(async () => {
      const cached = await caches.match(OFFLINE);
      return cached || Response.error();
    }),
  );
});
