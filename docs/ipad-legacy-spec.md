# Holy Writings — Making Main `holywritings.net` Work (and Work Offline) on iPad 2 / iOS 9.3.5

> **Handoff brief** prepared from live device testing & static audit of `devotional-pwa` (`c07a8f0be`).
> Goal: Make the **main `holywritings.net` site itself** run smoothly and work **offline from the Home Screen** on **iPad 2 (Wi-Fi, iOS 9.3.5 / WebKit 601.1.46 / 512 MB RAM / 1024×768)** — with zero source-code churn and zero impact on modern browsers.

---

## 1. Verified Target Environment

| Property | Value |
|---|---|
| Device | iPad 2 Wi-Fi, 512 MB RAM, 1024×768 (1× DPR) |
| OS / Browser | iOS 9.3.5 (`13G36`), `Safari/601.1` (`AppleWebKit/601.1.46`) |
| TLS to `holywritings.net` | Verified compatible: Cloudflare serves **TLS 1.2 + `ECDHE-ECDSA-AES128-GCM-SHA256`**, chained to **`GTS Root R4`** (Google Trust Services). Once `GTS Root R4` is installed on iOS 9.3.5, `https://holywritings.net` connects cleanly. |
| Home Screen install | Already configured in `head.html` (`apple-mobile-web-app-capable`, `apple-touch-icon`). Needs a tiny standalone-link interceptor so internal `<a href>` taps don't break out into Safari. |
| Offline shell caching | **No Service Worker** (arrived iOS 11.3). **HTML5 AppCache** (`<html manifest="/legacy.appcache">`) works natively on iOS 9.3.5 and is **completely ignored by all modern browsers** (removed in Chrome 95, Firefox 85, Safari 16.4), so it safely coexists with `/sw.js`! |
| Offline data storage | **`WebSQL`** (`window.openDatabase`, SQLite up to ~50 MB on iOS 9) + **`localStorage`** (~5 MB cap). Avoid IndexedDB on iOS 9 (buggy). |

---

## 2. Automated Audit of Current iOS 9.3.5 Blockers

### A. Parse-Time JS Syntax Errors (cannot be polyfilled — must be transpiled at build time)
Safari 9.0 fails to parse the entire `<script>` block on any of these:
* **Arrow functions `=>` (Safari 10+):** `devotional/list.html` (67), `search/list.html` (55), `daily/list.html` (45), `phelps/list.html` (33), `phelps/detail.html` (20), `static/js/program-view.js` (20), `quran/single.html` (14), `p/list.html` (10), `writings/single.html` (10), `bible/single.html` (9), `phelps/single.html` (9), `prayers/list.html` (4), `collections/list.html` (1).
* **`async` / `await` (Safari 10.1+):** `program-view.js` (37), `search/list.html` (24), `quran/single.html` (10), `daily/list.html` (9), `phelps/detail.html` (9), `bible/single.html` (8), `devotional/list.html` (7), `p/list.html` (7), `phelps/list.html` (3), `collections/list.html` (1).
* **Spread `...` (Safari 10+/11.1+):** `devotional/list.html` (5), `daily/list.html` (2), `program-view.js` (2), `phelps/list.html` (1), `search/list.html` (1).
* **Optional chaining `?.` (Safari 13.1+):** `quran/single.html` (2).
* **`let` / `const`:** Widespread across templates & `program-view.js` (Safari 9 only allows them in `'use strict'` and has TDZ/for-loop bugs).

### B. Missing Runtime Web APIs (100% polyfillable)
* **`window.fetch()` (Safari 10.1+):** `head.html`, `index.html`, `prayers/single.html`, `collections.js`, `prayer-list.js`, `program-view.js`, and most section pages.
* **`URLSearchParams` (Safari 10.1+):** `head.html`, `p/list.html`, `phelps/list.html`, `search/list.html`.
* **`NodeList.prototype.forEach` (Safari 10+):** `querySelectorAll(...).forEach(...)` in 14+ files (9× in `head.html`, 15× in `prayer-list.js`, 25× in `bible/single.html`).
* **`Object.entries` / `Object.values` / `Object.fromEntries` (Safari 10.1+):** `devotional/list.html`, `search/list.html`, `program-view.js`.
* **`IntersectionObserver` (Safari 12.1+):** `bible/single.html` (2), `writings/single.html` (1).
* **`navigator.clipboard` (Safari 13.1+):** `collections/list.html` (2), `devotional/list.html` (1).
* **`Element.prototype.scrollIntoView({behavior:...})` (Safari 15.4+):** `library/list.html`, `phelps/detail.html`, `writings/single.html`, `prayer-list.js`.
* **`String.prototype.padStart/padEnd/replaceAll`, `Array.prototype.includes/flat`:** Good defensive additions.

### C. CSS Gaps on Safari 9.0
* **`display: grid` (Safari 10.1+):** `assets/css/main.css` (6), `layouts/quality/list.html` (2), `layouts/library/list.html` (1).
* **Flex `gap:` (Safari 14.1+):** `assets/css/main.css` (25) + inline styles (`index.html`, `daily`, `devotional`, `collections`, `library`, `writings`, `quran`, `prayers`, `search`).
* **`position: sticky`:** Needs `-webkit-sticky` (4× in `main.css`).
* **8-digit hex:** `#ff795433` in `layouts/index.html` (1×).
* **`dvh` unit:** `assets/css/main.css` (1×).

---

## 3. Implementation Plan (4 Clean Pieces)

### Piece 1: Runtime Polyfill + Transparent WebSQL Offline Cache (`static/js/legacy-polyfill.js`)
Load **first** in `layouts/partials/head.html` (before the inline i18n `<script>`), **without `defer`**, written in strict ES5 (`var`, `function`):
* **Important `tsgo` note (`project_site_deploy.md`):** `cloudflare-build.sh` runs `npx -y @typescript/native-preview --noEmit` over `static/js/`! Either:
  * Put `// @ts-nocheck` at line 1 of `static/js/legacy-polyfill.js` (or keep it at `static/legacy-polyfill.js` outside `static/js/` so `tsgo` ignores it completely while staying clean).
* **Zero overhead on modern browsers:** Wrap every polyfill in a feature check (`if (!window.fetch) { ... }`).
* **Polyfills to include:**
  1. `NodeList.prototype.forEach` & `HTMLCollection.prototype.forEach`
  2. `URLSearchParams` (minimal `get`, `set`, `has`, `delete`, `toString`, `forEach`)
  3. `Object.values`, `Object.entries`, `Object.fromEntries`
  4. `Array.prototype.includes`, `String.prototype.includes/startsWith/endsWith/padStart/padEnd/replaceAll`
  5. `Element.prototype.closest`, `Element.prototype.matches`, `Element.prototype.remove`
  6. `IntersectionObserver` fallback (immediately invokes callback with `[{ isIntersecting: true, target: el }]` on `observe(el)` so lazy sections render)
  7. `Element.prototype.scrollIntoView` safe wrapper (catches or converts `{ behavior: 'smooth', block: ... }` object arg to boolean on Safari 9)
  8. `navigator.clipboard.writeText` fallback using hidden `<textarea>` + `document.execCommand('copy')`
  9. **Minimal `regeneratorRuntime`** (only loaded/defined if missing — required for transpiled `async`/`await`; ~2–3 KB minified, or include `regenerator-runtime/runtime.js` only when `typeof fetch === 'undefined'`)
  10. **iOS Home Screen standalone link handler:**
      ```js
      if (window.navigator && window.navigator.standalone) {
        document.addEventListener('click', function(e) {
          var el = e.target;
          while (el && el.tagName !== 'A') el = el.parentNode;
          if (!el || !el.href) return;
          if (el.target === '_blank' || el.hasAttribute('download')) return;
          if (el.origin !== location.origin) return;
          // Keep internal navigation inside full-screen Home Screen app
          e.preventDefault();
          location.href = el.href;
        }, false);
      }
      ```
  11. **`window.fetch` polyfill (XHR-based) WITH transparent WebSQL offline cache for `/data/*.json`:**
      * Only runs on browsers without native `fetch` (i.e. iOS 9–10, which also lack Service Workers!).
      * Opens `window.openDatabase('hw_offline_v1', '1.0', 'Holy Writings Offline', 50 * 1024 * 1024)` (with graceful fallback to plain XHR if WebSQL unavailable).
      * For same-origin `GET /data/*.json` requests:
        * Implements **stale-while-revalidate** (just like `sw.js`!) or network-first with WebSQL cache fallback:
        * On network success (and body starts with `{` or `[` — guarding against CF Pages 200 HTML fallback!), saves `(url, text, timestamp)` into WebSQL and resolves the Response (`{ ok: true, status: 200, text(), json(), clone() }`).
        * On network failure/offline, serves the cached JSON straight from WebSQL!
      * This gives the real `holywritings.net` automatic offline caching of every language/prayer/writing JSON visited on iOS 9.3.5!

### Piece 2: Fast Build-Time ES5 Transpilation (`scripts/transpile-es5.mjs` in `cloudflare-build.sh`)
Run **right after `hugo --minify`** in `cloudflare-build.sh`:
* Use **`@swc/core`** (super fast native binary, lowers `=>`, `async`/`await`, `let`/`const`, `...`, `?.`, destructuring, template literals all the way to pure ES5 with `regeneratorRuntime`—unlike `esbuild`, which leaves `function*` generators that Safari 9 cannot parse!). Alternatively `@babel/core` + `@babel/preset-env` (`targets: { ios: "9" }`).
* **Files to transpile:**
  1. `public/js/*.js` (skip `legacy-polyfill.js` and `sw.js` — note `sw.js` only runs on modern browsers with Service Worker support, so leave `public/sw.js` untouched!).
  2. Inline `<script>...</script>` blocks inside `public/**/*.html` (skip `<script type="application/ld+json">` and `<script src=...>` with empty body):
     * **Critical speed optimization:** Hash each inline script's exact text (SHA-256) into an in-memory `Map`. Even if `public/` contains thousands of HTML pages, there are only ~20–30 unique inline `<script>` templates across the whole site! Transpiling only unique scripts + writing back takes **~1–2 seconds total** on Cloudflare Pages.
* **Verify syntax:** Add a quick verification pass (e.g. `acorn` with `ecmaVersion: 5` on all unique transpiled scripts + `public/js/*.js`) so CI fails loudly if any ES6+ syntax slips through.
* **Optional nice-to-have:** Modern browsers vs legacy browsers — since `holywritings.net`'s JS is small vanilla scripts (~50 KB total), shipping ES5 everywhere is tiny, fast, and avoids serving dual bundles.

### Piece 3: Minimal CSS Fallbacks (`assets/css/main.css` + `layouts/index.html`)
1. Before each of the 6 `display: grid` rules in `assets/css/main.css` (and 3 inline grid rules in `quality/list.html` and `library/list.html`), add:
   ```css
   display: -webkit-flex;
   display: flex;
   -webkit-flex-wrap: wrap;
   flex-wrap: wrap;
   display: grid;
   ```
   *(And ensure grid children have a sensible fallback width/margin under `@supports not (display: grid)` so cards don't stretch edge-to-edge or stack awkwardly.)*
2. Add an `@supports not (gap: 1px)` block in `assets/css/main.css` adding small right/bottom margins to flex/grid children (`home-sections > *`, `.home-search > *`, nav links, action buttons).
3. Add `position: -webkit-sticky;` immediately before each `position: sticky;` (4 occurrences in `main.css`).
4. Replace `#ff795433` in `layouts/index.html` with `rgba(255, 121, 84, 0.2)`.
5. Add `height: 100vh;` immediately before the `100dvh` rule in `assets/css/main.css`.

### Piece 4: Legacy Offline AppCache (`static/legacy.appcache` + `static/_headers`)
Because Chrome (95+), Firefox (85+), and modern Safari (16.4+) completely removed HTML5 AppCache, `<html manifest="/legacy.appcache">` in `layouts/_default/baseof.html` is **100% invisible to modern browsers** while enabling offline Home Screen launch on iOS 9.3.5:
1. Create `static/legacy.appcache` (or generate its `# version <git-sha>` comment in `cloudflare-build.sh` so AppCache updates automatically on every deploy!):
   ```text
   CACHE MANIFEST
   # build 2026-11-22

   CACHE:
   /
   /daily/
   /prayers/
   /writings/
   /collections/
   /css/main.min.css
   /legacy-polyfill.js
   /js/collections.js
   /js/program-view.js
   /js/prayer-list.js
   /js/uuid-base36.js
   /js/render-md.js
   /js/fuzzy-ar.js
   /js/translit.js
   /icons/icon-180.png

   NETWORK:
   *
   ```
   *(Keep the `CACHE:` list lean—just the core shell pages & JS/CSS—so iOS 9 doesn't blow its AppCache quota! Any additional visited page is also auto-cached as a master entry by AppCache, and `/data/*.json` is cached in WebSQL by the `fetch` polyfill.)*
2. Create `static/_headers` (Cloudflare Pages currently has `static/_redirects`, no `static/_headers`):
   ```text
   /legacy.appcache
     Content-Type: text/cache-manifest; charset=utf-8
     Cache-Control: no-cache, no-store, must-revalidate
   ```
3. *(Optional UI sugar)* On `/collections/` or `/about/`, if `window.__hwLegacyCache` is present (iOS 9), show a small **"Offline cache (iOS 9): X MB — Download English / Clear cache"** card so the user can pre-fetch their favorite prayer language(s) before going offline!

---

## 4. Watch-Outs & Verification Gate

1. **`tsgo` pre-push gate (`project_site_deploy.md`):** Always run `npx -y @typescript/native-preview --noEmit` before pushing—`cloudflare-build.sh` fails the whole deploy on any type error in `static/js/`.
2. **Hugo template JS parser (`feedback_hugo_js.md`):** Keep `layouts/` free of `?.` / `??` (note: `layouts/quran/single.html` currently has 2 `?.` occurrences—worth double-checking!).
3. **512 MB RAM sanity check:** Very large pages (e.g. a full book with thousands of paragraphs or `inventory.json` at 11.8 MB / `search.json` at 9.1 MB) may still be heavy on an iPad 2; the polyfill + transpilation will make Home, Daily, Prayers, Collections, and normal Writings work cleanly, and `/downloads/` EPUBs remain a great fallback for huge books.
4. **Verify deployed bodies** (CF Pages returns HTTP `200` with SPA HTML on missing files!):
   * `curl -sI https://holywritings.net/legacy.appcache` → `content-type: text/cache-manifest`
   * `curl -s https://holywritings.net/legacy.appcache | head -n 5` → starts with `CACHE MANIFEST`
   * `curl -s https://holywritings.net/legacy-polyfill.js | head -n 5` → JS, not `<!doctype html>`
   * Check a transpiled page (e.g. `curl -s https://holywritings.net/daily/ | grep -c '=>'`) → `0`!
5. **Real-device test on iPad 2 (iOS 9.3.5):**
   * Visit `https://holywritings.net/` in Safari → wait ~2–3s for AppCache → **Share → Add to Home Screen** → open from Home Screen → visit Daily + your preferred Prayers language (populates WebSQL cache) → turn on **Airplane Mode** → relaunch from Home Screen and verify reading works offline!
