// prayer-list.js — Shared renderer for prayer-list views.
//
// One entry point: window.renderPrayerList(rootEl, opts).
// Renders /prayers/<code>/ for any code — language ("eo") or book ("eo-bp",
// "mul-na-bp"). There is no mode flag: every feature is data-driven.
//   Lang badge per card     → always (pulled from p.lang)
//   "Also in" lang switch   → iff p.translations is present
//   Book picker             → iff opts.bookSelectEl is given (host page owns the <select>)
//   Categories sidebar      → iff opts.categoriesEl is given
//   TOC chips               → iff opts.tocEl is given (or opts.toc=true)
//   Category H2s + ¶ anchors→ iff opts.showCategoryAnchor (default true)
//   Folding / ⛶ / ★ / +    → always
//
// "Open in language prayerbook" link only appears on cards where the prayer's
// language differs from opts.pageLang (e.g. multilingual book pages, or a
// post-switch revert state).
//
// Data shapes (do not modify these — owned by scripts/gen_hugo_data.go):
//   prayer in 'language' mode: {phelps, text, name, category, order_in_cat,
//     v|version, book_cats?: {<book>: {cat, cat_order}}, translations?: [...]}
//   prayer in 'book' mode:     {phelps, lang, lang_name, name, text, category,
//     order_in_cat, v|version}
//
// localStorage keys this module reads/writes:
//   hw_book_<lang>          — selected prayerbook for /prayers/<lang>/
//   hw_favorites            — array of phelps codes (★)
//   hw_devotional_codes     — array of {code, lang?, v?} or bare strings (+)

(function () {
  'use strict';

  var RTL_LANGS = new Set(['ar', 'fa', 'ur', 'he', 'ug']);
  var FAVS_KEY = 'hw_favorites';
  var DEV_KEY = 'hw_devotional_codes';

  function t(key, fallback) {
    if (typeof window.__t === 'function') {
      var v = window.__t(key);
      if (v && v !== key) return v;
    }
    return fallback != null ? fallback : key;
  }

  function md(text) {
    return window.renderMd ? window.renderMd(text || '') : (text || '');
  }

  function escapeHtml(s) {
    return String(s == null ? '' : s)
      .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
  }

  function escapeAttr(s) { return escapeHtml(s); }

  function slugCat(name) {
    return 'cat-' + String(name || '').toLowerCase()
      .replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '').slice(0, 60);
  }

  function splitPhelps(p) {
    var lower = String(p || '').toLowerCase();
    var m = lower.match(/^(.*?)([a-z]{3})$/);
    if (m) return { base: m[1], suffix: m[2] };
    return { base: lower, suffix: '' };
  }

  // Authors map: { "BH": { "en": "Bahá'u'lláh", "ru": "Бахаулла", ... }, ... }
  // Built from the i18n table by gen_hugo_data.go. Fetched once; cards
  // rendered before it arrives get their author backfilled.
  var AUTHORS = {};
  fetch('/data/authors.json').then(function (r) { return r.ok ? r.json() : {}; })
    .then(function (m) {
      AUTHORS = m || {};
      document.querySelectorAll('.prayer-author:empty').forEach(function (el) {
        var card = el.closest('.prayer-card');
        if (!card) return;
        var name = authorFromPin(card.dataset.phelps, card.dataset.lang);
        if (name) el.textContent = name;
      });
    });
  // Canonical author prefix = first 2 uppercase letters of the PIN.
  // BB/BBU → BB, BH/BHU → BH, AB/ABU → AB, UH/UHR/UHJ → UH, SE/SEGPB… → SE.
  function authorFromPin(pin, lang) {
    var m = String(pin || '').match(/^([A-Z]{2})/);
    if (!m) return '';
    var byLang = AUTHORS[m[1]];
    if (!byLang) return '';
    return byLang[lang] || byLang.en || '';
  }

  function wordCount(text, lang) {
    var plain = String(text || '').replace(/#+\s+[^\n]+/g, '');
    var isCJK = lang === 'ja' || lang === 'ko' || lang === 'zh-Hans' || lang === 'zh-Hant';
    if (isCJK) {
      var n = 0;
      // Count CJK ideographs + Hangul syllables; cheap approximation.
      for (var i = 0; i < plain.length; i++) {
        var c = plain.charCodeAt(i);
        if ((c >= 0x4E00 && c <= 0x9FFF) ||
            (c >= 0x3400 && c <= 0x4DBF) ||
            (c >= 0xAC00 && c <= 0xD7AF)) n++;
      }
      var unit = lang === 'ko' ? '자' : '字';
      return '~' + n + unit;
    }
    var matches = plain.match(/\S+/g);
    return '~' + (matches ? matches.length : 0) + 'w';
  }

  function previewOf(text) {
    var p = String(text || '').replace(/#+\s+[^\n]+/g, '')
      .replace(/[*#\[\]()>]/g, '').replace(/\s+/g, ' ').trim();
    if (p.length <= 60) return p;
    return p.slice(0, 60).replace(/\s+\S*$/, '') + '…';
  }

  // ── Build a single card ───────────────────────────────────────────
  function buildCard(p, opts) {
    var pin = p.phelps || '';
    var parts = splitPhelps(pin);
    // Display language is per-prayer when known (book pages carry p.lang per
    // entry; language pages don't but pageLang fills in). RTL follows the
    // resolved displayLang so multilingual books render Arabic prayers in RTL.
    var displayLang = p.lang || opts.pageLang || '';
    var langName = p.lang_name || displayLang;
    var rtl = RTL_LANGS.has(displayLang);
    var vid = p.v || p.version || '';
    var wcLabel = wordCount(p.text, displayLang);
    var preview = previewOf(p.text);
    var bookCats = p.book_cats || null;
    var hasTranslations = Array.isArray(p.translations) && p.translations.length;
    // Version variants: alt_sources holds same (phelps, lang) prayer from
    // other sources (bahaiprayers.app, llm-translation, …). We never
    // promote a text-less alt to primary — the data layer guarantees
    // text on the primary; alts with empty text become "view at source"
    // links rather than swappable text variants.
    var altSources = Array.isArray(p.alt_sources) ? p.alt_sources : [];
    var hasVersions = altSources.length > 0;
    // Page identity is fuzzy: `eo` matches eo prayers, `eo-bp` is a book whose
    // prayers are still in `eo`. The "open in language prayerbook" affordance
    // should appear when the prayer's lang isn't trivially the page itself.
    var showOpenInLang = displayLang && displayLang !== opts.pageLang;

    var card = document.createElement('article');
    card.className = 'prayer-card folded';
    card.id = pin;
    card.dataset.cat = p.category || '';
    card.dataset.nativeCat = p.category || '';
    card.dataset.nativeText = p.text || '';
    card.dataset.nativeName = p.name || '';
    card.dataset.nativeVersion = vid;
    card.dataset.displayLang = displayLang;
    card.dataset.phelps = pin;
    card.dataset.lang = displayLang;
    if (bookCats) card.dataset.bookCats = JSON.stringify(bookCats);
    if (hasVersions) {
      // Build a per-card version registry: primary first, then alts. Each
      // entry carries source label, version uuid+b36, text. The renderer
      // stores it on the card so the picker click handler can swap text
      // without re-walking arbitrary global state.
      var primarySource = p.source || 'unknown';
      var registry = [{
        source: primarySource, version: vid, v: vid,
        text: p.text || '', isPrimary: true,
      }];
      altSources.forEach(function (a) {
        var av = a.version || '';
        registry.push({
          source: a.source || 'unknown',
          version: av,
          v: av,
          text: a.text || '',
          isPrimary: false,
        });
      });
      card.dataset.versions = JSON.stringify(registry);
    }

    var header = document.createElement('div');
    header.className = 'prayer-card-header';

    var html = '';
    html += '<span class="prayer-toggle" aria-hidden="true">▶</span>';
    if (p.name) {
      html += '<span class="prayer-name">' + escapeHtml(p.name) + '</span>';
    }
    html += '<span class="prayer-preview">' + escapeHtml(preview) + '</span>';
    html += '<span class="word-count">' + escapeHtml(wcLabel) + '</span>';

    html += '<span class="prayer-phelps">';
    var href = '/phelps/' + parts.base + '/' + (parts.suffix ? '#' + parts.suffix : '');
    html += '<a href="' + escapeAttr(href) + '" title="All translations">' + escapeHtml(pin) + '</a>';
    // Link to /phelps/?pin=<base> directly (the legacy /inventory/?pin= path
    // is a meta-refresh redirect that doesn't carry the query through). Use
    // the base PIN (no suffix) so the inventory search-as-you-type pre-fills
    // with the prayer's family rather than one specific sub-code.
    html += ' <a href="/phelps/?pin=' + escapeAttr(parts.base.toUpperCase()) + '" title="Inventory entry" style="opacity:.6; font-size:.8em; margin-left:.3em">↗</a>';
    html += ' <span class="prayer-lang-badge" title="' + escapeAttr(t('prayer_lang_current_title', 'Currently shown in this language')) + '">' + escapeHtml(displayLang) + '</span>';
    if (hasTranslations) {
      html += ' <button class="prayer-revert-btn" type="button" title="' + escapeAttr(t('prayer_lang_revert_title', "Show again in the page's language")) + '" hidden>↶ ' + escapeHtml(t('prayer_lang_revert', 'revert')) + '</button>';
    }
    if (vid) {
      html += ' <a class="prayer-version-link" href="/p/?v=' + escapeAttr(vid) + '" title="' + escapeAttr(t('permalink_to_translation', 'Permalink to this exact translation')) + '" style="font-size:.8em; margin-left:.3em">\u{1F517}</a>';
    }
    html += '</span>';

    html += ' <button class="btn-expand prayer-expand" title="Full screen">⛶</button>';
    html += ' <button class="fav-btn" data-phelps="' + escapeAttr(pin) + '" aria-label="Toggle favourite">☆</button>';
    html += ' <button class="btn-add-devotional" data-phelps="' + escapeAttr(pin) + '" aria-label="Add to devotional" title="Add to devotional program">+</button>';

    if (showOpenInLang) {
      html += ' <a href="/prayers/' + escapeAttr(displayLang) + '/#' + escapeAttr(pin) + '" title="' + escapeAttr(t('prayer_open_in_book', 'Open in language prayerbook')) + '" style="opacity:.6; font-size:.8em; margin-left:.3em">↗</a>';
    }

    header.innerHTML = html;
    card.appendChild(header);

    var textEl = document.createElement('div');
    textEl.className = 'prayer-text';
    if (rtl) textEl.dir = 'rtl';
    textEl.innerHTML = md(p.text);
    card.appendChild(textEl);

    // Author signature under the prayer text. Aligns to the end (right in
    // LTR, left in RTL) — the classic "—Bahá'u'lláh" attribution style.
    // Content is filled by the AUTHORS fetch backfill if it hasn't resolved
    // yet at render time.
    var sigEl = document.createElement('div');
    sigEl.className = 'prayer-author';
    if (rtl) sigEl.dir = 'rtl';
    sigEl.textContent = authorFromPin(pin, displayLang);
    card.appendChild(sigEl);

    if (p.notes) {
      var notesEl = document.createElement('div');
      notesEl.className = 'prayer-notes';
      notesEl.textContent = p.notes;
      card.appendChild(notesEl);
    }

    if (p.source) {
      var meta = document.createElement('div');
      meta.className = 'prayer-meta';
      meta.innerHTML = '<span class="prayer-source">' + escapeHtml(p.source) + '</span>';
      card.appendChild(meta);
    }

    if (hasTranslations) {
      var tCount = p.translations.length;
      var tWrap = document.createElement('div');
      tWrap.className = 'prayer-translations';
      var listHtml = p.translations.map(function (tr) {
        var code = String(tr.language || '').toLowerCase();
        var lname = tr.lang_name || tr.language || '';
        return '<a href="/prayers/' + escapeAttr(code) + '/#' + escapeAttr(pin) + '"' +
          ' data-code="' + escapeAttr(code) + '"' +
          ' data-name="' + escapeAttr(String(lname).toLowerCase()) + '"' +
          ' data-lang-name="' + escapeAttr(lname) + '"' +
          ' class="lang-switch-link">' + escapeHtml(lname) +
          '<span class="lang-code-badge">' + escapeHtml(tr.language || '') + '</span></a>';
      }).join('');
      var filterInput = tCount > 6
        ? '<input type="search" class="lang-filter-input" placeholder="' + escapeAttr(t('filter_languages', 'Filter languages…')) + '" autocomplete="off">'
        : '';
      var langWord = tCount === 1 ? 'language' : t('languages', 'languages');
      tWrap.innerHTML =
        '<span class="trans-label">' + escapeHtml(t('also_in', 'Also in:')) + '</span>' +
        '<details class="lang-dropdown"><summary>' + tCount + ' ' + escapeHtml(langWord) + '</summary>' +
        '<div class="lang-dropdown-panel">' + filterInput +
        '<div class="lang-dropdown-list">' + listHtml + '</div></div></details>';
      card.appendChild(tWrap);
    }

    if (hasVersions) {
      // Version picker: same .prayer-translations / .lang-dropdown skeleton
      // as the "Also in" picker so it inherits the same CSS treatment.
      // Primary version is always first and active; text-less alts (e.g.
      // future scraped link-only entries) appear as disabled hints.
      var vCount = altSources.length + 1;
      var vWrap = document.createElement('div');
      vWrap.className = 'prayer-translations prayer-versions';
      var primaryLabel = sourceLabel(p.source || 'primary');
      var rows = ['<a href="#" class="version-swap-link active" data-version-idx="0">' +
        escapeHtml(primaryLabel) +
        ' <span class="lang-code-badge">' + escapeHtml(t('version_primary', 'primary')) + '</span></a>'];
      altSources.forEach(function (a, i) {
        var label = sourceLabel(a.source || 'unknown');
        var idx = i + 1;
        if (a.text) {
          rows.push('<a href="#" class="version-swap-link" data-version-idx="' + idx + '">' +
            escapeHtml(label) + '</a>');
        } else {
          rows.push('<a href="#" class="version-swap-link" data-version-idx="' + idx + '" style="opacity:.5; pointer-events:none">' +
            escapeHtml(label) + ' <span class="lang-code-badge">' + escapeHtml(t('version_no_text', 'link only')) + '</span></a>');
        }
      });
      vWrap.innerHTML =
        '<span class="trans-label">' + escapeHtml(t('versions_label', 'Version:')) + '</span>' +
        '<details class="lang-dropdown"><summary>' + vCount + ' ' +
        escapeHtml(vCount === 1 ? t('version_singular', 'source') : t('version_plural', 'sources')) +
        '</summary>' +
        '<div class="lang-dropdown-panel"><div class="lang-dropdown-list">' +
        rows.join('') + '</div></div></details>';
      card.appendChild(vWrap);
    }

    return card;
  }

  // Pretty-print known source identifiers for the version picker.
  function sourceLabel(s) {
    var map = {
      'bahaiprayers.net': 'bahaiprayers.net',
      'bahaiprayers.app': 'bahaiprayers.app',
      'llm-translation':  'LLM translation',
      'bahai.org':        'bahai.org',
      'reference.bahai.org': 'reference.bahai.org',
    };
    return map[s] || s;
  }

  // ── Category sidebar (language mode) ──────────────────────────────
  function buildCategoryList(state) {
    var sidebar = state.opts.categoriesEl;
    if (!sidebar) return;
    var catStats = {};
    var total = 0;
    state.cards.forEach(function (card) {
      total++;
      var cat = card.dataset.cat || '';
      if (!cat) return;
      if (!catStats[cat]) {
        var order = 9999;
        try {
          var bc = JSON.parse(card.dataset.bookCats || '{}') || {};
          if (bc[state.activeBook]) order = bc[state.activeBook].cat_order || 9999;
        } catch (e) {}
        catStats[cat] = { count: 0, order: order };
      }
      catStats[cat].count++;
    });

    if (state.activeCat && !catStats[state.activeCat]) state.activeCat = '';

    sidebar.innerHTML = '';
    var allLink = document.createElement('a');
    allLink.href = '#';
    allLink.dataset.cat = '';
    allLink.textContent = t('all_prayers', 'All prayers') + ' (' + total + ')';
    if (state.activeCat === '') allLink.className = 'active';
    allLink.addEventListener('click', function (e) {
      e.preventDefault();
      applyCategory(state, '');
    });
    sidebar.appendChild(allLink);

    Object.keys(catStats).map(function (k) {
      return [k, catStats[k]];
    }).sort(function (a, b) {
      return (a[1].order - b[1].order) || a[0].localeCompare(b[0]);
    }).forEach(function (pair) {
      var cat = pair[0], info = pair[1];
      var link = document.createElement('a');
      link.href = '#';
      link.dataset.cat = cat;
      link.textContent = cat + ' (' + info.count + ')';
      if (state.activeCat === cat) link.className = 'active';
      link.addEventListener('click', function (e) {
        e.preventDefault();
        applyCategory(state, cat);
      });
      sidebar.appendChild(link);
    });
  }

  function applyCategory(state, cat) {
    state.activeCat = cat;
    if (state.opts.categoriesEl) {
      state.opts.categoriesEl.querySelectorAll('a').forEach(function (l) {
        l.classList.toggle('active', l.dataset.cat === cat);
      });
    }
    state.cards.forEach(function (card) {
      card.classList.toggle('hidden', !(!cat || card.dataset.cat === cat));
    });
  }

  // ── Book switching (language mode only) ───────────────────────────
  function applyBook(state, book) {
    state.activeBook = book;
    if (state.opts.pageLang) {
      try { localStorage.setItem('hw_book_' + state.opts.pageLang, book); } catch (e) {}
    }
    var sel = state.opts.bookSelectEl;
    if (sel) sel.value = book;

    state.cards.forEach(function (card) {
      var cat = card.dataset.nativeCat || '';
      if (book) {
        try {
          var bc = JSON.parse(card.dataset.bookCats || '{}') || {};
          if (bc[book]) cat = bc[book].cat || '';
        } catch (e) {}
      }
      card.dataset.cat = cat;
    });
    buildCategoryList(state);
    applyCategory(state, state.activeCat);
  }

  function setupBookSelect(state) {
    var sel = state.opts.bookSelectEl;
    if (!sel) return;
    var totalPrayers = state.cards.length;
    var minHits = Math.max(5, Math.ceil(totalPrayers * 0.10));
    var alwaysShow = new Set([state.opts.defaultBook, state.activeBook].filter(Boolean));

    var hits = {};
    state.cards.forEach(function (card) {
      var bc = {};
      try { bc = JSON.parse(card.dataset.bookCats || '{}') || {}; } catch (e) {}
      Object.keys(bc).forEach(function (code) {
        hits[code] = (hits[code] || 0) + 1;
      });
    });

    Array.from(sel.options).forEach(function (opt) {
      var code = opt.value;
      var n = hits[code] || 0;
      var native = alwaysShow.has(code);
      if (!native && n < minHits) {
        opt.hidden = true;
        opt.disabled = true;
      } else {
        opt.textContent = opt.textContent.replace(/\s*\(\d+\/\d+\)\s*$/, '');
        opt.textContent = opt.textContent + ' (' + n + '/' + totalPrayers + ')';
      }
    });
    sel.addEventListener('change', function () {
      applyBook(state, sel.value);
    });
  }

  // ── Favourites ────────────────────────────────────────────────────
  function setupFavourites(root) {
    var favs;
    try { favs = new Set(JSON.parse(localStorage.getItem(FAVS_KEY) || '[]')); }
    catch (e) { favs = new Set(); }
    function paint() {
      root.querySelectorAll('.fav-btn').forEach(function (btn) {
        btn.textContent = favs.has(btn.dataset.phelps) ? '★' : '☆';
      });
    }
    paint();
    root.querySelectorAll('.fav-btn').forEach(function (btn) {
      btn.addEventListener('click', function (e) {
        e.stopPropagation();
        var p = btn.dataset.phelps;
        if (favs.has(p)) favs.delete(p); else favs.add(p);
        try { localStorage.setItem(FAVS_KEY, JSON.stringify(Array.from(favs))); } catch (e2) {}
        paint();
      });
    });
  }

  // ── Devotional buttons ────────────────────────────────────────────
  function setupDevotional(root, includeLang) {
    function getCodes() {
      try { return JSON.parse(localStorage.getItem(DEV_KEY) || '[]'); } catch (e) { return []; }
    }
    function codeStr(item) { return typeof item === 'string' ? item : item.code; }
    function langOf(item) { return typeof item === 'string' ? undefined : item.lang; }
    function flash(btn, text) {
      var orig = btn.textContent;
      btn.textContent = text;
      btn.disabled = true;
      setTimeout(function () { btn.textContent = orig; btn.disabled = false; }, 1200);
    }

    root.querySelectorAll('.btn-add-devotional').forEach(function (btn) {
      var card = btn.closest('.prayer-card');
      if (!card) return;
      var p = btn.dataset.phelps || card.dataset.phelps;
      var lng = includeLang ? card.dataset.lang : undefined;

      var items = getCodes();
      var matchExisting = items.some(function (i) {
        if (codeStr(i) !== p) return false;
        if (includeLang) return langOf(i) === lng;
        return true;
      });
      if (matchExisting) {
        btn.textContent = '✓';
        btn.classList.add('added');
      }

      btn.addEventListener('click', function (e) {
        e.stopPropagation();
        e.preventDefault();
        var its = getCodes();
        var found = its.findIndex(function (i) {
          if (codeStr(i) !== p) return false;
          if (includeLang) return langOf(i) === lng;
          return true;
        });
        if (found >= 0) {
          its.splice(found, 1);
          try { localStorage.setItem(DEV_KEY, JSON.stringify(its)); } catch (e2) {}
          btn.textContent = '+';
          btn.classList.remove('added');
        } else {
          var entry = includeLang ? { code: p, lang: lng } : { code: p };
          its.push(entry);
          try { localStorage.setItem(DEV_KEY, JSON.stringify(its)); } catch (e2) {}
          flash(btn, '✓');
          btn.classList.add('added');
        }
      });
    });
  }

  // ── In-place language switching (language mode) ───────────────────
  var _langCache = new Map();
  function loadLangData(code) {
    if (_langCache.has(code)) return Promise.resolve(_langCache.get(code));
    return fetch('/data/prayers/' + code + '.json').then(function (r) {
      if (!r.ok) throw new Error('HTTP ' + r.status);
      return r.json();
    }).then(function (data) {
      var idx = {};
      (data.prayers || []).forEach(function (p) { idx[p.phelps] = p; });
      var entry = { phelpsIndex: idx };
      _langCache.set(code, entry);
      return entry;
    }).catch(function (e) {
      console.warn('lang fetch failed', code, e);
      return null;
    });
  }

  function switchPrayerLanguage(card, targetLang, targetLangName, pageLang) {
    return loadLangData(targetLang).then(function (data) {
      if (!data) return false;
      var p = data.phelpsIndex[card.id];
      if (!p || !p.text) return false;
      var textEl = card.querySelector('.prayer-text');
      var nameEl = card.querySelector('.prayer-name');
      var badge = card.querySelector('.prayer-lang-badge');
      if (textEl) textEl.innerHTML = md(p.text);
      if (nameEl && p.name) nameEl.textContent = p.name;
      if (badge) {
        badge.textContent = targetLang;
        badge.title = 'Showing in ' + (targetLangName || targetLang);
        badge.classList.add('switched');
      }
      var revertBtn = card.querySelector('.prayer-revert-btn');
      if (revertBtn) {
        revertBtn.hidden = false;
        revertBtn.textContent = '↶ ' + pageLang;
        revertBtn.title = 'Switch back to ' + pageLang;
      }
      var vLink = card.querySelector('.prayer-version-link');
      if (vLink) {
        var id = p.v || p.version;
        if (id) vLink.href = '/p/?v=' + id;
      }
      card.dataset.displayLang = targetLang;
      if (textEl) textEl.dir = RTL_LANGS.has(targetLang) ? 'rtl' : 'ltr';
      return true;
    });
  }

  function revertPrayerCard(card, pageLang) {
    var badge = card.querySelector('.prayer-lang-badge');
    if (!badge || !badge.classList.contains('switched')) return;
    var textEl = card.querySelector('.prayer-text');
    var nameEl = card.querySelector('.prayer-name');
    var nativeText = card.dataset.nativeText || '';
    var nativeName = card.dataset.nativeName || '';
    if (textEl) {
      textEl.innerHTML = md(nativeText);
      textEl.dir = '';
    }
    if (nameEl) nameEl.textContent = nativeName;
    badge.textContent = pageLang;
    badge.classList.remove('switched');
    badge.title = t('prayer_lang_current_title', 'Currently shown in this language');
    card.dataset.displayLang = pageLang;
    var vLink = card.querySelector('.prayer-version-link');
    var nv = card.dataset.nativeVersion;
    if (vLink && nv) vLink.href = '/p/?v=' + nv;
    var revertBtn = card.querySelector('.prayer-revert-btn');
    if (revertBtn) revertBtn.hidden = true;
  }

  // Version swap: swap the prayer text in-place to an alternate source's
  // text. Updates the permalink (🔗) to point at the alt's version uuid.
  // The "active" class on the picker entry reflects which version is
  // currently displayed; clicking the same active link is a no-op.
  function setupVersionSwitching(root) {
    root.querySelectorAll('.version-swap-link').forEach(function (link) {
      link.addEventListener('click', function (e) {
        e.preventDefault();
        e.stopPropagation();
        var card = link.closest('.prayer-card');
        if (!card) return;
        var idx = parseInt(link.dataset.versionIdx || '0', 10);
        var registry;
        try { registry = JSON.parse(card.dataset.versions || '[]'); }
        catch (e2) { return; }
        if (!registry[idx] || !registry[idx].text) return;
        var v = registry[idx];
        var textEl = card.querySelector('.prayer-text');
        if (textEl) textEl.innerHTML = md(v.text);
        var vLink = card.querySelector('.prayer-version-link');
        if (vLink && v.v) vLink.href = '/p/?v=' + v.v;
        // Mark active picker entry.
        card.querySelectorAll('.version-swap-link').forEach(function (l) {
          l.classList.toggle('active', l === link);
        });
      });
    });
  }

  function setupLangSwitching(root, pageLang) {
    root.querySelectorAll('.lang-switch-link').forEach(function (link) {
      link.addEventListener('click', function (e) {
        if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey || e.button !== 0) return;
        var card = link.closest('.prayer-card');
        if (!card) return;
        var code = link.dataset.code;
        var name = link.dataset.langName || code;
        e.preventDefault();
        switchPrayerLanguage(card, code, name, pageLang).then(function (ok) {
          if (!ok) window.location.href = link.href;
          var dd = link.closest('.lang-dropdown');
          if (dd) dd.removeAttribute('open');
        });
      });
    });
    root.querySelectorAll('.prayer-lang-badge').forEach(function (badge) {
      badge.addEventListener('click', function (e) {
        e.stopPropagation();
        var card = badge.closest('.prayer-card');
        if (card) revertPrayerCard(card, pageLang);
      });
    });
    root.querySelectorAll('.prayer-revert-btn').forEach(function (btn) {
      btn.addEventListener('click', function (e) {
        e.stopPropagation();
        var card = btn.closest('.prayer-card');
        if (card) revertPrayerCard(card, pageLang);
      });
    });
    root.querySelectorAll('.lang-dropdown').forEach(function (dd) {
      var input = dd.querySelector('.lang-filter-input');
      if (!input) return;
      var links = Array.from(dd.querySelectorAll('.lang-dropdown-list a'));
      input.addEventListener('input', function () {
        var q = input.value.toLowerCase().trim();
        links.forEach(function (a) {
          a.style.display = (!q || a.dataset.code.includes(q) || a.dataset.name.includes(q)) ? '' : 'none';
        });
      });
    });
    document.addEventListener('click', function (e) {
      root.querySelectorAll('.lang-dropdown[open]').forEach(function (dd) {
        if (!dd.contains(e.target)) dd.removeAttribute('open');
      });
    });
  }

  // ── Folding ───────────────────────────────────────────────────────
  function setupFolding(root) {
    root.querySelectorAll('.prayer-card-header').forEach(function (header) {
      header.addEventListener('click', function (e) {
        if (e.target.closest('a') || e.target.closest('.fav-btn') ||
            e.target.closest('.btn-add-devotional') ||
            e.target.closest('.btn-expand') ||
            e.target.closest('.prayer-revert-btn')) return;
        header.closest('.prayer-card').classList.toggle('folded');
      });
    });
  }

  // ── Full-screen overlay ───────────────────────────────────────────
  function setupFullScreen(root) {
    root.querySelectorAll('.prayer-expand').forEach(function (btn) {
      btn.addEventListener('click', function (e) {
        e.stopPropagation();
        var card = btn.closest('.prayer-card');
        if (!card) return;
        var textEl = card.querySelector('.prayer-text');
        var nameEl = card.querySelector('.prayer-name');
        if (!textEl) return;
        card.classList.remove('folded');
        var overlay = document.createElement('div');
        overlay.className = 'expanded-overlay';
        var rtlAttr = (textEl.dir === 'rtl' || card.closest('[dir="rtl"]')) ? ' dir="rtl"' : '';
        overlay.innerHTML = '<button class="expand-close">✖</button>' +
          (nameEl ? '<h2 style="margin-bottom:1rem;font-size:1.2rem">' + escapeHtml(nameEl.textContent) + '</h2>' : '') +
          '<div' + rtlAttr + '>' + textEl.innerHTML + '</div>';
        document.body.appendChild(overlay);
        overlay.querySelector('.expand-close').addEventListener('click', function () { overlay.remove(); });
        var esc = function (ev) {
          if (ev.key === 'Escape') {
            overlay.remove();
            document.removeEventListener('keydown', esc);
          }
        };
        document.addEventListener('keydown', esc);
      });
    });
  }

  // ── TOC + category H2s (book mode) ────────────────────────────────
  function buildBookGroupedHtml(prayers, opts) {
    // Group by category in order of appearance.
    var cats = [];
    var idx = {};
    prayers.forEach(function (p) {
      var k = p.category || '(uncategorised)';
      if (!(k in idx)) {
        idx[k] = cats.length;
        cats.push({ name: k, prayers: [] });
      }
      cats[idx[k]].prayers.push(p);
    });
    return cats;
  }

  function renderTOC(cats, mountEl) {
    if (!mountEl || !cats.length) return;
    var label = cats.length + ' ' +
      (cats.length === 1 ? t('book_category', 'category') : t('book_categories', 'categories')) +
      ' — ' + t('book_jump_to', 'jump to:');
    var chips = cats.map(function (c) {
      var slug = slugCat(c.name);
      return '<a href="#' + slug + '" class="book-toc-chip">' +
        escapeHtml(c.name) +
        ' <span class="book-toc-count">(' + c.prayers.length + ')</span></a>';
    }).join('');
    mountEl.innerHTML =
      '<details class="book-toc" open>' +
        '<summary>' + label + '</summary>' +
        '<div class="book-toc-chips">' + chips + '</div>' +
      '</details>';
  }

  // ── Hash jump ─────────────────────────────────────────────────────
  function handleHash(root) {
    var hash = location.hash.slice(1);
    if (!hash) return;
    // Category slug?
    if (/^cat-/.test(hash)) {
      var catEl = document.getElementById(hash);
      if (catEl) {
        setTimeout(function () {
          catEl.scrollIntoView({ behavior: 'smooth', block: 'start' });
        }, 80);
        return;
      }
    }
    var target = document.getElementById(hash);
    if (target && target.classList.contains('prayer-card')) {
      target.classList.remove('folded');
      setTimeout(function () {
        target.scrollIntoView({ behavior: 'smooth', block: 'start' });
      }, 100);
    }
  }

  // ── Main entry point ──────────────────────────────────────────────
  function renderPrayerList(rootEl, opts) {
    if (!rootEl) return;
    opts = opts || {};
    if (opts.pageLang == null) opts.pageLang = '';
    if (opts.showCategoryAnchor == null) opts.showCategoryAnchor = true;

    var prayers = opts.prayers || [];

    rootEl.innerHTML = '';

    var state = {
      opts: opts,
      cards: [],
      activeBook: opts.defaultBook || '',
      activeCat: ''
    };

    // Always group by category (in order of appearance). For pages with one
    // category this collapses to a single section; for multi-category pages
    // we get H2s + an optional TOC. ¶ permalink anchors live on each H2.
    var grouped = buildBookGroupedHtml(prayers, opts);

    if (opts.tocEl || opts.toc) {
      var tocEl = opts.tocEl;
      if (!tocEl) {
        tocEl = document.createElement('div');
        rootEl.appendChild(tocEl);
      }
      renderTOC(grouped, tocEl);
    }

    grouped.forEach(function (cat) {
      if (opts.showCategoryAnchor && grouped.length > 1) {
        var slug = slugCat(cat.name);
        var h = document.createElement('h2');
        h.id = slug;
        h.style.cssText = 'margin-top:1.5rem; font-size:1.1rem; border-bottom:1px solid var(--border); padding-bottom:.3rem';
        h.innerHTML = escapeHtml(cat.name) +
          ' <span style="color:var(--text-secondary); font-weight:400; font-size:.85rem">(' + cat.prayers.length + ')</span>' +
          ' <a href="#' + slug + '" style="font-size:.7em; opacity:.4; text-decoration:none" title="Permalink to this category">¶</a>';
        rootEl.appendChild(h);
      }
      cat.prayers.forEach(function (p) {
        var card = buildCard(p, opts);
        state.cards.push(card);
        rootEl.appendChild(card);
      });
    });

    // Migrate localStorage book preference, if a default book is given.
    if (opts.defaultBook && opts.pageLang) {
      try {
        var key = 'hw_book_' + opts.pageLang;
        var stored = localStorage.getItem(key);
        var known = new Set((opts.allBooks || []).map(function (b) { return b.code; }));
        if (!stored || !known.has(stored)) {
          stored = opts.defaultBook;
          if (stored) localStorage.setItem(key, stored);
        }
        state.activeBook = stored || opts.defaultBook || '';
      } catch (e) {
        state.activeBook = opts.defaultBook || '';
      }
    }

    if (opts.bookSelectEl) setupBookSelect(state);
    if (opts.categoriesEl) applyBook(state, state.activeBook);

    setupFolding(rootEl);
    setupFullScreen(rootEl);
    setupFavourites(rootEl);
    // Devotional entries always carry the prayer's lang since cards may mix
    // languages (multilingual books) and the same phelps in two different
    // languages should be distinct devotional items.
    setupDevotional(rootEl, true);
    setupLangSwitching(rootEl, opts.pageLang);
    setupVersionSwitching(rootEl);

    if (typeof window.__translatePage === 'function') window.__translatePage();
    if (typeof window.__markUiLang === 'function') window.__markUiLang();

    setTimeout(function () { handleHash(rootEl); }, 50);
    window.addEventListener('hashchange', function () { handleHash(rootEl); });

    return state;
  }

  window.renderPrayerList = renderPrayerList;
})();
