// FlexDex service worker: small on purpose.
//
// It keeps the offline page, the app icons and the two main fonts, and shows the offline page when
// a page cannot load. It never caches pages or API data: everything a trainer sees stays live
// (raids, events, their own data), and nothing signed in is ever stored on the device.
// Served from /sw.js so its scope is the whole site. Bump VERSION when the cached files change.
const VERSION = 'flexdex-shell-1';
const OFFLINE = '/static/app/offline.html';
const PRECACHE = [
  OFFLINE,
  '/static/app/icons/logo.svg',
  '/static/app/icons/icon-192.png',
  '/static/fonts/figtree-latin.woff2',
  '/static/fonts/bricolage-grotesque-latin.woff2',
];

self.addEventListener('install', (event) => {
  event.waitUntil(caches.open(VERSION).then((c) => c.addAll(PRECACHE)).then(() => self.skipWaiting()));
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== VERSION).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  );
});

self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET') return;
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;

  // Pages: always the network; the offline page only when the network fails.
  if (req.mode === 'navigate') {
    event.respondWith(fetch(req).catch(() => caches.match(OFFLINE)));
    return;
  }

  // Fonts and app icons never change under the same URL: serve them from the cache when present.
  if (url.pathname.startsWith('/static/fonts/') || url.pathname.startsWith('/static/app/icons/')) {
    event.respondWith(caches.match(req).then((hit) => hit || fetch(req)));
  }
  // Everything else (API, data, scripts, styles) goes straight to the network as if no worker
  // were installed.
});
