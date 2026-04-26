// Holy Writings — Service Worker
// Strategy: cache what the user visits, serve offline when network unavailable

// Bumped when SHELL_URLS changes so old caches get cleared on activate.
const CACHE_VERSION = 'hw-v3';
const SHELL_CACHE = CACHE_VERSION + '-shell';
const DATA_CACHE = CACHE_VERSION + '-data';
const PAGE_CACHE = CACHE_VERSION + '-pages';

// App shell: always pre-cached on install
const SHELL_URLS = [
  '/',
  '/daily/',
  '/devotional/',
  '/prayers/',
  '/p/',
  '/writings/',
  // Index needed for /p/?v= to resolve permalinks on first offline visit.
  // Per-prayer-collection JSONs are fetched on-demand and cached via the
  // stale-while-revalidate fetch handler.
  '/data/prayerbooks.json',
  '/data/version_index.json',
  '/fonts/nine-star.woff2',
  '/favicon.svg',
];

// ── Install: pre-cache shell ──
self.addEventListener('install', function(e) {
  e.waitUntil(
    caches.open(SHELL_CACHE).then(function(cache) {
      return cache.addAll(SHELL_URLS);
    }).then(function() {
      return self.skipWaiting();
    })
  );
});

// ── Activate: clean old caches ──
self.addEventListener('activate', function(e) {
  e.waitUntil(
    caches.keys().then(function(keys) {
      return Promise.all(
        keys.filter(function(k) {
          return k.startsWith('hw-') && k !== SHELL_CACHE && k !== DATA_CACHE && k !== PAGE_CACHE;
        }).map(function(k) { return caches.delete(k); })
      );
    }).then(function() {
      return self.clients.claim();
    })
  );
});

// ── Fetch: smart strategy per resource type ──
self.addEventListener('fetch', function(e) {
  var url = new URL(e.request.url);

  // Skip non-GET and cross-origin
  if (e.request.method !== 'GET') return;
  if (url.origin !== self.location.origin) return;

  var path = url.pathname;

  // Data JSONs (prayers, writings, languages): stale-while-revalidate
  if (path.startsWith('/data/') && path.endsWith('.json')) {
    e.respondWith(staleWhileRevalidate(e.request, DATA_CACHE));
    return;
  }

  // Static assets (fonts, icons, CSS): cache-first
  if (path.match(/\.(woff2|png|svg|css|ico)$/) || path.startsWith('/fonts/') || path.startsWith('/android/') || path.startsWith('/ios/') || path.startsWith('/windows11/')) {
    e.respondWith(cacheFirst(e.request, SHELL_CACHE));
    return;
  }

  // HTML pages: network-first, cache fallback
  e.respondWith(networkFirst(e.request, PAGE_CACHE));
});

// ── Cache strategies ──

// Cache-first: use cache, only fetch if not cached
function cacheFirst(request, cacheName) {
  return caches.match(request).then(function(cached) {
    if (cached) return cached;
    return fetch(request).then(function(response) {
      if (response.ok) {
        var clone = response.clone();
        caches.open(cacheName).then(function(cache) { cache.put(request, clone); });
      }
      return response;
    });
  });
}

// Network-first: try network, fall back to cache
function networkFirst(request, cacheName) {
  return fetch(request).then(function(response) {
    if (response.ok) {
      var clone = response.clone();
      caches.open(cacheName).then(function(cache) { cache.put(request, clone); });
    }
    return response;
  }).catch(function() {
    return caches.match(request).then(function(cached) {
      return cached || new Response('Offline — this page has not been cached yet.', {
        status: 503,
        headers: { 'Content-Type': 'text/html' }
      });
    });
  });
}

// Stale-while-revalidate: serve cache immediately, fetch update in background
function staleWhileRevalidate(request, cacheName) {
  return caches.open(cacheName).then(function(cache) {
    return cache.match(request).then(function(cached) {
      var fetchPromise = fetch(request).then(function(response) {
        if (response.ok) cache.put(request, response.clone());
        return response;
      }).catch(function() { return cached; });

      return cached || fetchPromise;
    });
  });
}
