// collections.js — Named collections of saved prayers & writings.
//
// Google-bookmarks-style model: two built-in collections (Favorites,
// Read later) plus any number of user-named ones. Saving an item opens
// a picker where it can be ticked into any subset of collections.
//
// One global: window.hwCollections.
//   list()                      → [{id, name, builtin, items, created, modified}]
//   get(id)                     → collection or null
//   create(name)                → new collection (id auto)
//   rename(id, name) / removeCollection(id)   — builtins refuse both
//   add(id, item) / removeItem(id, code, lang) / has(id, code, lang)
//   isFavorite(code) / toggleFavorite(item)   — mirrors legacy hw_favorites
//   openPicker(items)           → "Save to…" modal; items = item or [item]
//   toHash(items) / shareUrl(items)           → devotional program hash/link
//   parseList(text)             → items from a program link, code list, or
//                                 a text export produced by exportText()
//   exportText(col)             → Promise<string> with resolved full texts
//
// Item shape: {code, lang?, v?, title?, w?, added}
//   code  — phelps (BH00074BLE) or shorthand reference (iqan:1:3, bible:john:3:16)
//   lang  — display language lock (same semantics as devotional items)
//   v     — base36 version uuid (exact-translation lock)
//   title — display title captured at save time (keeps the list readable
//           without fetching data files)
//   w     — writings collection key (e.g. "gleanings") when the phelps code
//           lives in /data/writings/<w>/ rather than /data/prayers/
//
// localStorage:
//   hw_collections — {version:1, collections:[...]}
//   hw_favorites   — legacy array of phelps codes; kept in sync with the
//                    Favorites collection so older code keeps working.

(function () {
  'use strict';

  var KEY = 'hw_collections';
  var FAVS_KEY = 'hw_favorites';
  var REF_PREFIXES = ['bible', 'quran', 'iqan', 'aqdas', 'gleanings', 'hw', 'pm',
    'tdp', 'esw', 'tab', 'gpb', 'gems', 'swb', 'swab', 'pup'];

  function t(key, fallback) {
    if (typeof window.__t === 'function') {
      var v = window.__t(key);
      if (v && v !== key) return v;
    }
    return fallback != null ? fallback : key;
  }

  function escapeHtml(s) {
    return String(s == null ? '' : s)
      .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
  }

  // ── Storage ────────────────────────────────────────────────────────
  function emptyState() {
    var now = Date.now();
    return {
      version: 1,
      collections: [
        { id: 'favorites', builtin: true, name: '', items: [], created: now, modified: now },
        { id: 'readlater', builtin: true, name: '', items: [], created: now, modified: now }
      ]
    };
  }

  function load() {
    var state = null;
    try { state = JSON.parse(localStorage.getItem(KEY) || 'null'); } catch (e) {}
    if (!state || !Array.isArray(state.collections)) state = emptyState();
    // Ensure builtins exist (front of list, favorites first)
    ['readlater', 'favorites'].forEach(function (id) {
      if (!state.collections.some(function (c) { return c.id === id; })) {
        state.collections.unshift({ id: id, builtin: true, name: '', items: [], created: Date.now(), modified: Date.now() });
      }
    });
    // One-time merge of the legacy favourites star list
    try {
      var legacy = JSON.parse(localStorage.getItem(FAVS_KEY) || '[]');
      if (Array.isArray(legacy) && legacy.length) {
        var fav = state.collections.find(function (c) { return c.id === 'favorites'; });
        legacy.forEach(function (code) {
          if (!fav.items.some(function (i) { return i.code === code; })) {
            fav.items.push({ code: code, added: Date.now() });
          }
        });
      }
    } catch (e) {}
    return state;
  }

  function save(state) {
    try { localStorage.setItem(KEY, JSON.stringify(state)); } catch (e) {}
    // Mirror Favorites back into the legacy key (codes only)
    var fav = state.collections.find(function (c) { return c.id === 'favorites'; });
    if (fav) {
      try { localStorage.setItem(FAVS_KEY, JSON.stringify(fav.items.map(function (i) { return i.code; }))); } catch (e) {}
    }
  }

  function displayName(col) {
    if (col.id === 'favorites') return t('col_favorites', 'Favorites');
    if (col.id === 'readlater') return t('col_readlater', 'Read later');
    return col.name || col.id;
  }

  function itemKey(code, lang) { return code + '|' + (lang || ''); }

  function normalizeItem(item) {
    if (typeof item === 'string') item = { code: item };
    return {
      code: item.code,
      lang: item.lang || undefined,
      v: item.v || undefined,
      title: item.title || undefined,
      w: item.w || undefined,
      added: item.added || Date.now()
    };
  }

  // `iqan:1:3` and `BH00002:1:3` are the SAME grammar — an identity followed
  // by structural segments — differing only in whether the head is a friendly
  // alias or a PIN. So this asks "is the head an alias?", not "is there a
  // colon?". The old contains-a-colon test predates Phelps codes using the
  // separator and would now misread BH00002:1:1 as a range reference, and
  // refs are never resolved to text: saved items would silently lose theirs.
  function isRef(code) {
    return REF_PREFIXES.indexOf(String(code || '').split(':')[0].toLowerCase()) >= 0;
  }

  // ── Mutations ──────────────────────────────────────────────────────
  function create(name) {
    var state = load();
    var col = {
      id: 'c' + Date.now().toString(36) + Math.floor(Math.random() * 1296).toString(36),
      name: String(name || '').trim() || 'Collection',
      items: [], created: Date.now(), modified: Date.now()
    };
    state.collections.push(col);
    save(state);
    return col;
  }

  // ── Offline ────────────────────────────────────────────────────────
  // Saving is a promise that the item will be there later — including on a
  // plane. The item's title is already stored inline, but its TEXT lives in a
  // data file that may never have been fetched, so pull it into a cache the
  // service worker protects from version cleanup (SAVED_CACHE in sw.js).
  // Best-effort and non-blocking: saving must never fail because caching did.
  function itemDataUrls(item) {
    if (!item || isRef(item.code)) return [];   // range refs stay links
    var lang = (item.lang || 'en').toLowerCase();
    var urls = ['/data/prayers/' + lang + '.json'];
    if (item.w) urls.unshift('/data/writings/' + item.w + '/' + lang + '.json');
    return urls;
  }

  function cacheItemOffline(item) {
    if (typeof caches === 'undefined') return Promise.resolve();
    var urls = itemDataUrls(item);
    if (!urls.length) return Promise.resolve();
    return caches.open('hw-saved').then(function (cache) {
      return Promise.all(urls.map(function (u) {
        // Individually, so one 404 (e.g. a language with no writings file)
        // doesn't discard the rest the way cache.addAll would.
        return fetch(u).then(function (r) {
          return r.ok ? cache.put(u, r.clone()) : null;
        }).catch(function () { return null; });
      }));
    }).catch(function () {});
  }

  // Can the app itself boot with no network? A per-item "available offline"
  // tick is a lie if the shell won't load, so every item check is gated on
  // this. Deliberately conservative: the pages a saved item is read through.
  var SHELL_PROBE = ['/', '/collections/', '/p/', '/data/version_index.json'];
  var _shellReady = null;
  function shellReady() {
    if (_shellReady) return _shellReady;
    if (typeof caches === 'undefined') return (_shellReady = Promise.resolve(false));
    _shellReady = Promise.all(SHELL_PROBE.map(function (u) {
      return caches.match(u).then(function (r) { return !!r; }).catch(function () { return false; });
    })).then(function (hits) {
      return hits.every(Boolean);
    }).catch(function () { return false; });
    return _shellReady;
  }

  // Is this item readable with no network? True when the shell can boot AND a
  // data file that could resolve it is cached. File-level granularity is the
  // right answer for the data half: if the file is cached, resolveText() will
  // find the entry in it.
  function isItemOffline(item) {
    if (typeof caches === 'undefined') return Promise.resolve(false);
    return shellReady().then(function (ok) {
      if (!ok) return false;
      var urls = itemDataUrls(item);
      if (!urls.length) return true;   // range refs are links, not fetched text
      return Promise.all(urls.map(function (u) {
        return caches.match(u).then(function (r) { return !!r; }).catch(function () { return false; });
      })).then(function (hits) { return hits.some(Boolean); });
    });
  }

  // {ready, total, offline} for a collection — what the page badge reports.
  function offlineStatus(col) {
    var items = (col && col.items) || [];
    if (!items.length) return Promise.resolve({ ready: 0, total: 0, offline: true });
    return Promise.all(items.map(isItemOffline)).then(function (flags) {
      var ready = flags.filter(Boolean).length;
      return { ready: ready, total: items.length, offline: ready === items.length };
    });
  }

  function cacheCollection(col) {
    var items = (col && col.items) || [];
    return Promise.all(items.map(cacheItemOffline));
  }

  // Every data file every saved item needs, deduped — typically one or two
  // however many items are saved, since they share language files.
  function cacheAllSaved() {
    if (typeof caches === 'undefined') return Promise.resolve();
    var seen = {};
    load().collections.forEach(function (c) {
      (c.items || []).forEach(function (i) {
        itemDataUrls(i).forEach(function (u) { seen[u] = 1; });
      });
    });
    var urls = Object.keys(seen);
    if (!urls.length) return Promise.resolve();
    return caches.open('hw-saved').then(function (cache) {
      return Promise.all(urls.map(function (u) {
        return fetch(u).then(function (r) {
          return r.ok ? cache.put(u, r.clone()) : null;
        }).catch(function () { return null; });
      }));
    }).catch(function () {});
  }

  function addItem(id, item) {
    var state = load();
    var col = state.collections.find(function (c) { return c.id === id; });
    if (!col) return false;
    item = normalizeItem(item);
    if (!col.items.some(function (i) { return itemKey(i.code, i.lang) === itemKey(item.code, item.lang); })) {
      col.items.push(item);
      col.modified = Date.now();
      save(state);
      cacheItemOffline(item);
    }
    return true;
  }

  function removeItem(id, code, lang) {
    var state = load();
    var col = state.collections.find(function (c) { return c.id === id; });
    if (!col) return false;
    var before = col.items.length;
    col.items = col.items.filter(function (i) { return itemKey(i.code, i.lang) !== itemKey(code, lang); });
    if (col.items.length !== before) { col.modified = Date.now(); save(state); }
    return true;
  }

  function hasItem(id, code, lang) {
    var col = load().collections.find(function (c) { return c.id === id; });
    if (!col) return false;
    // Lang-less query matches any saved language (the star button on prayer
    // cards asks per-code, not per-translation).
    return col.items.some(function (i) {
      return i.code === code && (lang === undefined || (i.lang || '') === (lang || ''));
    });
  }

  // ── Program hash (same encoding as devotional/list.html) ──────────
  function toHash(items) {
    return items.map(function (i) {
      var c = i.code;
      // Reference codes use dots in URL hash (colons conflict with lang suffix)
      if (c.indexOf(':') >= 0 && !c.match(/^[A-Z]/)) c = c.replace(/:/g, '.');
      var s = c;
      if (i.lang) s += ':' + i.lang;
      if (i.v) s += ':v_' + i.v;
      return s;
    }).join(',');
  }

  function shareUrl(items) {
    return location.origin + '/devotional/#' + toHash(items);
  }

  // ── Import parsing ─────────────────────────────────────────────────
  function parseToken(s) {
    s = s.trim();
    if (!s || s.charAt(0) === '@') return null;
    var lc = s.toLowerCase();
    // Shorthand reference, dot or colon form
    for (var ri = 0; ri < REF_PREFIXES.length; ri++) {
      var p = REF_PREFIXES[ri];
      if (lc.indexOf(p + '.') === 0 || lc.indexOf(p + ':') === 0) {
        return { code: lc.replace(/\./g, ':') };
      }
    }
    // Phelps code with optional :lang / :v_<base36> suffixes
    var parts = s.split(':');
    if (!/^[a-z]{2,4}[0-9]{4,6}[a-z]{0,3}$/i.test(parts[0])) return null;
    var item = { code: parts[0].toUpperCase() };
    for (var pi = 1; pi < parts.length; pi++) {
      var seg = parts[pi];
      if (seg.indexOf('v_') === 0) item.v = seg.substring(2);
      else if (!item.lang) item.lang = seg.toLowerCase();
    }
    return item;
  }

  function parseList(text) {
    text = String(text || '');
    var items = [];
    var seen = {};
    function push(item, title) {
      if (!item) return;
      if (title) item.title = title;
      var k = itemKey(item.code, item.lang);
      if (!seen[k]) { seen[k] = true; items.push(item); }
    }
    // Text-export form: "## CODE[:lang] — Title" headers are authoritative
    var headers = text.match(/^##\s+\S+.*$/gm);
    if (headers && headers.length) {
      headers.forEach(function (h) {
        var m = h.match(/^##\s+(\S+)(?:\s+[—-]\s+(.*))?$/);
        if (m) push(parseToken(m[1]), m[2] ? m[2].trim() : undefined);
      });
      return items;
    }
    // Otherwise: URLs contribute their hash fragment; everything is tokenized
    // on commas/whitespace/newlines.
    text = text.replace(/https?:\/\/\S+/g, function (url) {
      var hi = url.indexOf('#');
      return hi >= 0 ? ' ' + url.substring(hi + 1).replace(/,/g, ' ') + ' ' : ' ';
    });
    text.split(/[\s,]+/).forEach(function (tok) { push(parseToken(tok)); });
    return items;
  }

  // ── Text export (best-effort full-text resolution) ─────────────────
  var _jsonCache = {};
  function fetchJson(url) {
    if (_jsonCache[url]) return _jsonCache[url];
    _jsonCache[url] = fetch(url).then(function (r) { return r.ok ? r.json() : null; }).catch(function () { return null; });
    return _jsonCache[url];
  }

  function resolveText(item) {
    if (isRef(item.code)) return Promise.resolve(null); // range refs stay links
    var lang = (item.lang || 'en').toLowerCase();
    var tries = [];
    if (item.w) {
      tries.push(fetchJson('/data/writings/' + item.w + '/' + lang + '.json').then(function (d) {
        if (!d || !d.books) return null;
        for (var bi = 0; bi < d.books.length; bi++) {
          var es = d.books[bi].entries || [];
          for (var ei = 0; ei < es.length; ei++) {
            if (es[ei].phelps === item.code) return es[ei].text || null;
          }
        }
        return null;
      }));
    }
    tries.push(fetchJson('/data/prayers/' + lang + '.json').then(function (d) {
      if (!d || !d.prayers) return null;
      var p = d.prayers.find(function (x) { return x.phelps === item.code; });
      return p ? p.text || null : null;
    }));
    return tries.reduce(function (chain, next) {
      return chain.then(function (got) { return got || next; });
    }, Promise.resolve(null));
  }

  function exportText(col, defaultLang) {
    var name = displayName(col);
    var head = '# ' + name + ' — ' + location.host + '\n# ' + shareUrl(col.items) + '\n';
    return Promise.all(col.items.map(function (item) {
      var rItem = item.lang || !defaultLang ? item :
        { code: item.code, lang: defaultLang, w: item.w };
      return resolveText(rItem).then(function (text) {
        var header = '## ' + item.code + (item.lang ? ':' + item.lang : '') +
          (item.title ? ' — ' + item.title : '');
        var body = text ? text.trim() : '(' + shareUrl([item]) + ')';
        return header + '\n\n' + body + '\n';
      });
    })).then(function (blocks) {
      return head + '\n' + blocks.join('\n');
    });
  }

  // ── "Save to…" picker ──────────────────────────────────────────────
  var CSS = '' +
    '.colpick-overlay{position:fixed;inset:0;background:rgba(0,0,0,.45);z-index:1000;display:flex;align-items:center;justify-content:center;padding:1rem}' +
    '.colpick{background:var(--bg,#fff);color:inherit;border:1px solid var(--border,#ccc);border-radius:10px;box-shadow:0 8px 32px rgba(0,0,0,.35);width:100%;max-width:340px;padding:1rem 1.1rem;font-size:.95rem}' +
    '.colpick h3{margin:0 0 .7rem;font-size:1.05rem}' +
    '.colpick-list{max-height:45vh;overflow-y:auto;margin-bottom:.7rem}' +
    '.colpick-list label{display:flex;align-items:center;gap:.5rem;padding:.35rem .2rem;cursor:pointer;border-radius:6px}' +
    '.colpick-list label:hover{background:var(--bg-secondary,rgba(128,128,128,.1))}' +
    '.colpick-count{margin-left:auto;font-size:.8rem;color:var(--text-secondary,#888)}' +
    '.colpick-new{display:flex;gap:.4rem;margin-bottom:.8rem}' +
    '.colpick-new input{flex:1;min-width:0;padding:.35rem .5rem;border:1px solid var(--border,#ccc);border-radius:6px;background:inherit;color:inherit}' +
    '.colpick-btn{background:none;border:1px solid var(--border,#ccc);border-radius:6px;padding:.35rem .7rem;cursor:pointer;color:inherit}' +
    '.colpick-btn:hover{border-color:var(--accent,#ff7954);color:var(--accent,#ff7954)}' +
    '.colpick-footer{display:flex;justify-content:flex-end}' +
    '.btn-save-col{background:none;border:none;cursor:pointer;font-size:1rem;padding:0 .15rem;opacity:.65;vertical-align:middle}' +
    '.btn-save-col:hover,.btn-save-col.saved{opacity:1}';

  function injectCss() {
    if (document.getElementById('colpick-css')) return;
    var st = document.createElement('style');
    st.id = 'colpick-css';
    st.textContent = CSS;
    document.head.appendChild(st);
  }

  function openPicker(items, onChange) {
    if (!Array.isArray(items)) items = [items];
    items = items.map(normalizeItem);
    if (!items.length) return;
    injectCss();

    var overlay = document.createElement('div');
    overlay.className = 'colpick-overlay';
    var box = document.createElement('div');
    box.className = 'colpick';
    overlay.appendChild(box);

    function close() {
      overlay.remove();
      document.removeEventListener('keydown', onKey);
      if (onChange) onChange();
    }
    function onKey(/** @type {KeyboardEvent} */ e) { if (e.key === 'Escape') close(); }

    function render() {
      var state = load();
      var rows = state.collections.map(function (col) {
        var allIn = items.every(function (it) {
          return col.items.some(function (i) { return itemKey(i.code, i.lang) === itemKey(it.code, it.lang); });
        });
        return '<label><input type="checkbox" data-col="' + escapeHtml(col.id) + '"' + (allIn ? ' checked' : '') + '>' +
          '<span>' + escapeHtml(displayName(col)) + '</span>' +
          '<span class="colpick-count">' + col.items.length + '</span></label>';
      }).join('');
      box.innerHTML =
        '<h3>' + escapeHtml(t('col_save_to', 'Save to…')) + '</h3>' +
        '<div class="colpick-list">' + rows + '</div>' +
        '<div class="colpick-new">' +
          '<input type="text" placeholder="' + escapeHtml(t('col_new_collection', 'New collection')) + '" maxlength="60">' +
          '<button class="colpick-btn colpick-create">' + escapeHtml(t('col_create', 'Create')) + '</button>' +
        '</div>' +
        '<div class="colpick-footer"><button class="colpick-btn colpick-done">' + escapeHtml(t('col_done', 'Done')) + '</button></div>';

      box.querySelectorAll('input[type="checkbox"]').forEach(function (/** @type {HTMLInputElement} */ cb) {
        cb.addEventListener('change', function () {
          items.forEach(function (it) {
            if (cb.checked) addItem(cb.dataset.col, it);
            else removeItem(cb.dataset.col, it.code, it.lang);
          });
          render();
        });
      });
      var nameInput = /** @type {HTMLInputElement} */ (box.querySelector('.colpick-new input'));
      function createAndTick() {
        var name = nameInput.value.trim();
        if (!name) return;
        var col = create(name);
        items.forEach(function (it) { addItem(col.id, it); });
        render();
      }
      box.querySelector('.colpick-create').addEventListener('click', createAndTick);
      nameInput.addEventListener('keydown', function (/** @type {KeyboardEvent} */ e) { if (e.key === 'Enter') createAndTick(); });
      box.querySelector('.colpick-done').addEventListener('click', close);
    }

    overlay.addEventListener('click', function (e) { if (e.target === overlay) close(); });
    document.addEventListener('keydown', onKey);
    render();
    document.body.appendChild(overlay);
  }

  // Styles are needed as soon as any page shows a 🔖 button, not just when
  // the picker opens.
  injectCss();

  // ── Public API ─────────────────────────────────────────────────────
  window.hwCollections = {
    list: function () { return load().collections; },
    get: function (id) { return load().collections.find(function (c) { return c.id === id; }) || null; },
    displayName: displayName,
    create: create,
    rename: function (id, name) {
      var state = load();
      var col = state.collections.find(function (c) { return c.id === id; });
      if (!col || col.builtin) return false;
      col.name = String(name || '').trim() || col.name;
      col.modified = Date.now();
      save(state);
      return true;
    },
    removeCollection: function (id) {
      var state = load();
      var col = state.collections.find(function (c) { return c.id === id; });
      if (!col || col.builtin) return false;
      state.collections = state.collections.filter(function (c) { return c.id !== id; });
      save(state);
      return true;
    },
    add: addItem,
    // Replace a collection's whole item list (program-view widget edits —
    // lang locks, version pins, reorder, remove — persist through here).
    setItems: function (id, items) {
      var state = load();
      var col = state.collections.find(function (c) { return c.id === id; });
      if (!col) return false;
      col.items = (items || []).map(normalizeItem);
      col.modified = Date.now();
      save(state);
      return true;
    },
    removeItem: removeItem,
    has: hasItem,
    isFavorite: function (code) { return hasItem('favorites', code); },
    toggleFavorite: function (item) {
      item = normalizeItem(item);
      if (hasItem('favorites', item.code)) {
        // Remove every saved language variant of this code from Favorites
        var state = load();
        var fav = state.collections.find(function (c) { return c.id === 'favorites'; });
        fav.items = fav.items.filter(function (i) { return i.code !== item.code; });
        fav.modified = Date.now();
        save(state);
        return false;
      }
      addItem('favorites', item);
      return true;
    },
    isRef: isRef,
    resolveText: resolveText,
    toHash: toHash,
    shareUrl: shareUrl,
    parseList: parseList,
    exportText: exportText,
    openPicker: openPicker,
    cacheItemOffline: cacheItemOffline,
    cacheCollection: cacheCollection,
    cacheAllSaved: cacheAllSaved,
    isItemOffline: isItemOffline,
    offlineStatus: offlineStatus,
    shellReady: shellReady
  };

  // Backfill: items saved before save-time caching existed have no cached
  // data file, so the first offline read would still fail. The URL set
  // dedupes hard (one file per language, per writings collection), so this
  // is a handful of requests however many prayers are saved. Deferred to
  // idle so it never competes with rendering.
  // Guarded on `caches` so the module has no side effect (and leaves no timer
  // running) in environments without the Cache API — jsdom under jest, for one.
  if (typeof window !== 'undefined' && typeof caches !== 'undefined' &&
      navigator.onLine !== false) {
    var kick = function () { cacheAllSaved(); };
    if (typeof requestIdleCallback === 'function') requestIdleCallback(kick, { timeout: 8000 });
    else setTimeout(kick, 3000);
  }
})();
