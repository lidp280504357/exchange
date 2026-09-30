// The mobile site's service worker (design §7.1): a page opened without a
// network shows the "网络不可用" page instead of the browser's error.
// Nothing else is cached: data is live, and hashed files are cached by HTTP.
const CACHE = "m-offline-v1";

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches
      .open(CACHE)
      .then((cache) => cache.add("/offline.html"))
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
  if (event.request.mode !== "navigate") return;
  event.respondWith(fetch(event.request).catch(() => caches.match("/offline.html")));
});
