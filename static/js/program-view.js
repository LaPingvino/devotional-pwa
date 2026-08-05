// program-view.js — Shared devotional-item engine + list widget.
//
// Extracted verbatim from layouts/devotional/list.html so /devotional/ and
// /collections/ render and edit item lists identically (Joop: "isolate a view
// and edit collection widget that can be used by both").
//
// Two layers:
//   1. Resolution engine (module-level, shared caches):
//      resolveCode(code, lang) — phelps codes, writing shorthands (iqan:,
//      aqdas:, hw:, pm:, tab:, esw:, tdp:, gpb:, gems:, swb:, swab:, pup:),
//      bible:/quran: refs (with Bolls.life translations), XX-translit virtual
//      languages, version-locked items (v → uuid → exact translation).
//   2. hwProgramView.create(cfg) — the item-list view/editor: per-item header
//      (drag-reorder, code, meta link, fullscreen w/ zoom, language lock,
//      version pin, permalink, remove), text with author signature, optional
//      comparison columns.
//
// create(cfg) options:
//   container         element the items render into            (required)
//   emptyEl           element toggled when the list is empty   (optional)
//   getItems()        → live array of {code, lang?, v?, …}     (required)
//                       The widget mutates entries/array in place (lock,
//                       unpin, splice, reorder) — extra fields survive.
//   onChange(items)   called after any mutation; persist here  (optional)
//   getLang()         → current global display language        (required)
//   getAllLanguages() → languages.json array (rtl/name lookup) (required)
//   getCompareLangs() → comparison language codes              (optional)
//   removable         show per-item remove × (default true)
//
// CSS lives in assets/css/main.css ("Program-view widget" section).
// Depends on: window.renderMd, window.base36ToUuid/uuidToBase36 (sync),
// window.__transliterate (optional, for -translit langs).

(function () {
  'use strict';

  // ── Shared caches ──────────────────────────────────────────────────
  let prayerCache = {};   // lang → {phelps → prayer}
  let writingCache = {};  // `${key}/${lang}` → {phelps → entry}
  var bibleCache = {};    // book → {chapters: [...]}
  var quranCache = {};    // lang → {surahs: [...]}
  var bollsDevCache = {};
  let _versionIndex = null;
  let _writingsIndex = null;
  let _writingsIndexPromise = null;

  // Virtual transliteration languages — handled by resolveCode via -translit suffix
  var TRANSLIT_ENTRIES = [
    {code: 'ar-translit', name: 'Arabic (Latin)', nameLC: 'arabic latin translit transliteration transliterated'},
    {code: 'fa-translit', name: 'Persian (Latin)', nameLC: 'persian farsi latin translit transliteration transliterated'},
  ];
  function isTranslitCode(c) { return c && c.endsWith('-translit'); }

  function escapeHtml(s) {
    return String(s == null ? '' : s)
      .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
  }

  function renderMd(text) {
    return window.renderMd ? window.renderMd(text || '') : (text || '');
  }

  // ── Authors (i18n table via /data/authors.json) ───────────────────
  let AUTHORS = {};
  fetch('/data/authors.json').then(r => r.ok ? r.json() : {}).then(m => { AUTHORS = m || {}; });
  function authorFromPin(pin, lang) {
    const m = String(pin || '').match(/^([A-Z]{2})/);
    if (!m) return '';
    const byLang = AUTHORS[m[1]];
    if (!byLang) return '';
    return byLang[lang] || byLang['default'] || byLang.en || '';
  }
  // Signature form for "— <Author>" lines under prayer texts: per-lang _sig
  // override (e.g. «ع ع», 'Abdu'l-Bahá's actual Tablet signature) wins.
  function sigFromPin(pin, lang) {
    const m = String(pin || '').match(/^([A-Z]{2})/);
    if (!m) return '';
    const byLang = AUTHORS[m[1]];
    if (!byLang) return '';
    const sig = byLang._sig || {};
    return sig[lang] || authorFromPin(pin, lang);
  }
  // Build an attribution string per the active UI language's author_attribution
  // template (e.g. "by {name}", "von {name}", "{name}著"). Falls back to ": <name>".
  function formatAttribution(name) {
    if (!name) return '';
    const tpl = (window.__t && window.__t('author_attribution')) || '';
    if (tpl && tpl !== 'author_attribution' && tpl.indexOf('{name}') >= 0) {
      return tpl.replace('{name}', name);
    }
    return ': ' + name;
  }

  // ── Writings index (lazy) ──────────────────────────────────────────
  function getWritingsIndex() {
    if (_writingsIndex) return Promise.resolve(_writingsIndex);
    if (!_writingsIndexPromise) {
      _writingsIndexPromise = fetch('/data/writings.json')
        .then(r => r.ok ? r.json() : [])
        .then(idx => { _writingsIndex = idx || []; return _writingsIndex; })
        .catch(() => { _writingsIndex = []; return _writingsIndex; });
    }
    return _writingsIndexPromise;
  }

  // ── Version index for version-locked items (item.v) ──
  // /data/version_index.json maps UUID → [lang, phelps]; we lazy-load and cache.
  async function getVersionIndex() {
    if (_versionIndex !== null) return _versionIndex;
    try {
      const r = await fetch('/data/version_index.json');
      _versionIndex = r.ok ? await r.json() : {};
    } catch { _versionIndex = {}; }
    return _versionIndex;
  }

  // Resolve an item that has v set: decode base36 → UUID → (lang, phelps),
  // fetch that lang's prayer map, return a resolved entry. Returns null if
  // the version doesn't match (corrupted base36, deleted prayer, etc.).
  async function resolveVersionLocked(item) {
    if (!item.v || !window.base36ToUuid) return null;
    const uuid = window.base36ToUuid(item.v);
    if (!uuid) return null;
    const idx = await getVersionIndex();
    const info = idx[uuid];
    if (!info) return null;
    const [verLang, verPhelps] = info;
    const pMap = await getPrayerMap(verLang);
    const p = pMap[verPhelps];
    if (!p) return null;
    return {
      text: p.text,
      category: p.category,
      type: 'prayer',
      name: p.name,
      link: '/p/?v=' + item.v,
      _verLang: verLang,
      _verPhelps: verPhelps,
    };
  }

  // ── Fetch prayer data for a language ──
  async function getPrayerMap(lang) {
    if (prayerCache[lang]) return prayerCache[lang];
    try {
      const res = await fetch('/data/prayers/' + lang.toLowerCase() + '.json');
      if (!res.ok) { prayerCache[lang] = {}; return {}; }
      const data = await res.json();
      const map = {};
      (data.prayers || []).forEach(p => { map[p.phelps] = p; });
      prayerCache[lang] = map;
      return map;
    } catch { prayerCache[lang] = {}; return {}; }
  }

  // ── Fetch writing data for a type+language ──
  async function getWritingMap(key, lang) {
    const cacheKey = key + '/' + lang;
    if (writingCache[cacheKey]) return writingCache[cacheKey];
    try {
      const res = await fetch('/data/writings/' + key + '/' + lang.toLowerCase() + '.json');
      if (!res.ok) { writingCache[cacheKey] = {}; return {}; }
      const data = await res.json();
      const map = {};
      const all = [];
      (data.books || []).forEach(b => {
        (b.entries || []).forEach(e => {
          const entry = { ...e, bookTitle: b.title };
          all.push(entry);
          // For types where entries share phelps (e.g. GPB paragraphs): merge text so
          // a direct phelps lookup yields the full chapter instead of only the last paragraph.
          if (!map[e.phelps]) {
            map[e.phelps] = entry;
          } else {
            map[e.phelps] = { ...map[e.phelps], text: (map[e.phelps].text || '') + '\n' + (entry.text || '') };
          }
        });
      });
      Object.defineProperty(map, '_all', { value: all, enumerable: false });
      writingCache[cacheKey] = map;
      return map;
    } catch { writingCache[cacheKey] = {}; return {}; }
  }

  async function fetchBibleBook(book) {
    if (bibleCache[book]) return bibleCache[book];
    try {
      var res = await fetch('/data/bible/' + book + '.json');
      if (!res.ok) { bibleCache[book] = null; return null; }
      bibleCache[book] = await res.json();
      return bibleCache[book];
    } catch(e) { bibleCache[book] = null; return null; }
  }

  async function fetchQuran(lang) {
    var key = lang || 'en';
    if (quranCache[key]) return quranCache[key];
    try {
      var res = await fetch('/data/quran/' + key + '.json');
      if (!res.ok) {
        // Fall back to English
        if (key !== 'en') return fetchQuran('en');
        quranCache[key] = null; return null;
      }
      quranCache[key] = await res.json();
      return quranCache[key];
    } catch(e) { quranCache[key] = null; return null; }
  }

  // Parse bible:book:chapter:verse or bible:book:chapter:start-end
  function parseBibleRef(code) {
    var parts = code.split(':');
    if (parts.length < 3) return null;
    var book = parts[1];
    var chapter = parseInt(parts[2]);
    var verseStart = 1, verseEnd = 999;
    if (parts[3]) {
      var vp = parts[3].split('-');
      verseStart = parseInt(vp[0]) || 1;
      verseEnd = vp[1] ? parseInt(vp[1]) : verseStart;
    }
    return { book: book, chapter: chapter, verseStart: verseStart, verseEnd: verseEnd };
  }

  // Parse quran:surah:ayah or quran:surah:start-end
  function parseQuranRef(code) {
    var parts = code.split(':');
    if (parts.length < 2) return null;
    var surah = parseInt(parts[1]);
    var ayahStart = 1, ayahEnd = 999;
    if (parts[2]) {
      var vp = parts[2].split('-');
      ayahStart = parseInt(vp[0]) || 1;
      ayahEnd = vp[1] ? parseInt(vp[1]) : ayahStart;
    }
    return { surah: surah, ayahStart: ayahStart, ayahEnd: ayahEnd };
  }

  // ── Bolls.life Bible translations ──
  var BOLLS_LANG_MAP = {
    fr:'FRLSG',de:'LUT',es:'RV1960',pt:'ARA',ru:'SYNOD',it:'NR06',nl:'DSV',pl:'BG',
    ro:'VDCL',hu:'KB',sv:'SFB2015',ko:'KRV',ja:'JPKJV','zh-Hant':'CUV','zh-Hans':'CUNPS',
    id:'TB',vi:'VI1934',no:'DNB',cs:'CSP09',fa:'POV',hi:'HIOV',sw:'SUV',af:'AFR53',la:'VULG'
  };
  var BOLLS_BOOK_MAP = {
    genesis:1,exodus:2,leviticus:3,numbers:4,deuteronomy:5,joshua:6,judges:7,ruth:8,
    '1-samuel':9,'2-samuel':10,'1-kings':11,'2-kings':12,'1-chronicles':13,'2-chronicles':14,
    ezra:15,nehemiah:16,esther:17,job:18,psalms:19,proverbs:20,ecclesiastes:21,
    'song-of-solomon':22,isaiah:23,jeremiah:24,lamentations:25,ezekiel:26,daniel:27,
    hosea:28,joel:29,amos:30,obadiah:31,jonah:32,micah:33,nahum:34,habakkuk:35,
    zephaniah:36,haggai:37,zechariah:38,malachi:39,
    matthew:40,mark:41,luke:42,john:43,acts:44,romans:45,
    '1-corinthians':46,'2-corinthians':47,galatians:48,ephesians:49,philippians:50,
    colossians:51,'1-thessalonians':52,'2-thessalonians':53,'1-timothy':54,'2-timothy':55,
    titus:56,philemon:57,hebrews:58,james:59,'1-peter':60,'2-peter':61,
    '1-john':62,'2-john':63,'3-john':64,jude:65,revelation:66
  };

  async function fetchBollsVerses(bollsCode, book, chapter, verseStart, verseEnd) {
    var bookNum = BOLLS_BOOK_MAP[book];
    if (!bookNum) return null;
    var key = bollsCode + ':' + bookNum + ':' + chapter;
    var verses;
    if (bollsDevCache[key]) {
      verses = bollsDevCache[key];
    } else {
      try {
        var url = 'https://bolls.life/get-text/' + bollsCode + '/' + bookNum + '/' + chapter + '/';
        var res = await fetch(url);
        if (!res.ok) return null;
        verses = await res.json();
        bollsDevCache[key] = verses;
      } catch(e) { return null; }
    }
    return verses.filter(function(v) { return v.verse >= verseStart && v.verse <= verseEnd; });
  }

  // ── Transliterate resolved text (for XX-translit virtual languages) ──
  function transliterateResolved(resolved) {
    if (!resolved || !resolved.text || typeof window.__transliterate !== 'function') return resolved;
    var paras = resolved.text.split(/\n\n+/);
    var tParts = [];
    for (var i = 0; i < paras.length; i++) {
      var plain = paras[i].replace(/<[^>]+>/g, '').replace(/[#*_>\[\]()]/g, '').trim();
      if (!plain) continue;
      var tr = window.__transliterate(plain);
      tParts.push((tr.html || tr.text || plain));
    }
    var newText = tParts.join('\n\n') || resolved.text;
    return { text: newText, category: resolved.category, type: resolved.type, name: resolved.name, link: resolved.link, _transliterated: true };
  }

  // ── Resolve a code to text in given language ──
  async function resolveCode(code, lang) {
    // Handle XX-translit virtual languages: resolve base language, then transliterate
    if (lang && lang.endsWith('-translit')) {
      var baseLang = lang.replace(/-translit$/, '');
      var baseResolved = await resolveCode(code, baseLang);
      return transliterateResolved(baseResolved);
    }

    // Bible reference
    if (code.startsWith('bible:')) {
      var ref = parseBibleRef(code);
      if (!ref) return null;

      // Try Bolls translation for non-en/ar languages
      var bollsCode = BOLLS_LANG_MAP[lang];
      if (bollsCode) {
        var bVerses = await fetchBollsVerses(bollsCode, ref.book, ref.chapter, ref.verseStart, ref.verseEnd);
        if (bVerses && bVerses.length > 0) {
          var textParts = bVerses.map(function(v) {
            return '<sup>' + v.verse + '</sup> ' + (v.text || '');
          });
          var bookName = ref.book.replace(/-/g, ' ').replace(/\b\w/g, function(c) { return c.toUpperCase(); });
          var title = bookName + ' ' + ref.chapter;
          if (ref.verseEnd < 999) {
            title += ':' + ref.verseStart;
            if (ref.verseEnd > ref.verseStart) title += '-' + ref.verseEnd;
          }
          return { text: '<p>' + textParts.join(' ') + '</p>', category: 'Bible', type: 'bible', name: title, link: '/bible/' + ref.book + '/#ch' + ref.chapter };
        }
      }

      // Fall back to local KJV/Van Dyck data
      var data = await fetchBibleBook(ref.book);
      if (!data) return null;
      var ch = (data.chapters || []).find(function(c) { return c.chapter === ref.chapter; });
      if (!ch) return null;
      var verses = (ch.verses || []).filter(function(v) {
        return v.num >= ref.verseStart && v.num <= ref.verseEnd;
      });
      if (verses.length === 0) return null;
      var textParts = verses.map(function(v) {
        var t = {he: v.he, syr: v.syr, el: v.el, ar: v.ar, en: v.en}[lang] || v.en || v.ar || '';
        return '<sup>' + v.num + '</sup> ' + t;
      });
      var bookName = (data.book && data.book.conventional) || ref.book;
      var title = bookName.charAt(0).toUpperCase() + bookName.slice(1) + ' ' + ref.chapter;
      if (ref.verseEnd < 999) {
        title += ':' + ref.verseStart;
        if (ref.verseEnd > ref.verseStart) title += '-' + ref.verseEnd;
      }
      return { text: '<p>' + textParts.join(' ') + '</p>', category: 'Bible', type: 'bible', name: title, link: '/bible/' + ref.book + '/#ch' + ref.chapter };
    }

    // Quran reference
    if (code.startsWith('quran:')) {
      var qref = parseQuranRef(code);
      if (!qref) return null;
      var data = await fetchQuran(lang);
      if (!data) return null;
      var surah = (data.surahs || []).find(function(s) { return s.number === qref.surah; });
      if (!surah) return null;
      var ayahs = (surah.verses || []).filter(function(v) {
        return v.ayah >= qref.ayahStart && v.ayah <= qref.ayahEnd;
      });
      if (ayahs.length === 0) return null;
      var textParts = ayahs.map(function(v) {
        return '<sup>' + v.ayah + '</sup> ' + (v.text || '');
      });
      var qtitle = surah.name_trans || ('Surah ' + qref.surah);
      if (qref.ayahEnd < 999) {
        qtitle += ' ' + qref.ayahStart;
        if (qref.ayahEnd > qref.ayahStart) qtitle += '-' + qref.ayahEnd;
      }
      var quranLang = lang === 'en' ? 'en' : lang;
      return { text: '<p>' + textParts.join(' ') + '</p>', category: "Qur'an", type: 'quran', name: qtitle, link: '/quran/' + quranLang + '/#surah-' + qref.surah };
    }

    // Writing shorthand: iqan:part:section, aqdas:para, etc.
    var writingShort = parseWritingShorthand(code);
    if (writingShort) {
      var wMap = await getWritingMap(writingShort.key, lang);
      if (!wMap) wMap = await getWritingMap(writingShort.key, 'en');
      if (wMap) {
        var entries = wMap._all || Object.values(wMap);
        var matching = entries.filter(writingShort.filter);
        if (matching.length > 0) {
          var text = matching.map(function(e) { return e.text; }).join('\n');
          return { text: text, category: writingShort.title, type: 'writing', name: writingShort.label, link: '/writings/' + writingShort.key + '/' + lang + '/' };
        }
      }
    }

    // Try prayers first
    var pMap = await getPrayerMap(lang);
    if (pMap[code]) {
      var p = pMap[code];
      return { text: p.text, category: p.category, type: 'prayer', name: p.name, link: '/prayers/' + lang + '/#' + code, version: p.version };
    }

    // Try writings — check each writing type
    var wIdx = await getWritingsIndex();
    for (var wi = 0; wi < wIdx.length; wi++) {
      var w = wIdx[wi];
      if (w.langs.some(function(l) { return l.code === lang; })) {
        var wMap = await getWritingMap(w.key, lang);
        if (wMap[code]) {
          var e = wMap[code];
          return { text: e.text, category: w.title, type: 'writing', name: e.name || e.bookTitle, link: '/writings/' + w.key + '/' + lang + '/#' + code };
        }
      }
    }

    return null;
  }

  // Generate Phelps codes for a range, given prefix and padding
  function phelpsRange(prefix, start, end, pad) {
    var codes = [];
    for (var i = start; i <= end; i++) {
      var s = String(i);
      while (s.length < pad) s = '0' + s;
      codes.push(prefix + s);
    }
    return codes;
  }

  // Parse writing shorthands like iqan:1:5, aqdas:42, gleanings:131
  function parseWritingShorthand(code) {
    var m;
    // iqan:part:section or iqan:part:start-end
    m = code.match(/^iqan:(\d+):(\d+)(?:-(\d+))?$/);
    if (m) {
      var part = m[1], start = parseInt(m[2]), end = m[3] ? parseInt(m[3]) : start;
      var wanted = phelpsRange('BH00002' + part, start, end, 3);
      return {
        key: 'iqan', title: "Kitáb-i-Íqán",
        label: "Íqán " + part + ":" + start + (end > start ? '–' + end : ''),
        filter: function(e) { return wanted.indexOf(e.phelps) >= 0; }
      };
    }
    // aqdas:para or aqdas:start-end
    m = code.match(/^aqdas:(\d+)(?:-(\d+))?$/);
    if (m) {
      var start = parseInt(m[1]), end = m[2] ? parseInt(m[2]) : start;
      var wanted = phelpsRange('BH00001', start, end, 3);
      return {
        key: 'aqdas', title: "Kitáb-i-Aqdas",
        label: "Aqdas ¶" + start + (end > start ? '–' + end : ''),
        filter: function(e) { return wanted.indexOf(e.phelps) >= 0; }
      };
    }
    // gleanings:num or gleanings:start-end
    m = code.match(/^gleanings:(\d+)(?:-(\d+))?$/);
    if (m) {
      var start = parseInt(m[1]), end = m[2] ? parseInt(m[2]) : start;
      var wanted = phelpsRange('BH10200', start, end, 3);
      return {
        key: 'gleanings', title: "Gleanings",
        label: "Gleanings " + start + (end > start ? '–' + end : ''),
        filter: function(e) { return wanted.indexOf(e.phelps) >= 0; }
      };
    }
    // hw:a5 or hw:p12 (Hidden Words Arabic/Persian)
    m = code.match(/^hw:([ap])(\d+)(?:-(\d+))?$/);
    if (m) {
      var section = m[1] === 'a' ? 'Arabic' : 'Persian';
      var start = parseInt(m[2]), end = m[3] ? parseInt(m[3]) : start;
      var prefix = m[1] === 'a' ? 'BH00386A' : 'BH00113P';
      var wanted = phelpsRange(prefix, start, end, 2);
      return {
        key: 'hidden-words', title: "Hidden Words",
        label: section + " Hidden Word" + (end > start ? 's ' + start + '–' + end : ' ' + start),
        filter: function(e) { return wanted.indexOf(e.phelps) >= 0; }
      };
    }
    // pm:42 (Prayers & Meditations section)
    m = code.match(/^pm:(\d+)(?:-(\d+))?$/);
    if (m) {
      var start = parseInt(m[1]), end = m[2] ? parseInt(m[2]) : start;
      return {
        key: 'pm', title: "Prayers & Meditations",
        label: "PM " + start + (end > start ? '–' + end : ''),
        filter: function(e) { return e.order >= start && e.order <= end; }
      };
    }
    // tab:BH00053:5 or tab:BH00053:10-20 (Tablets of Bahá'u'lláh)
    m = code.match(/^tab:([a-zA-Z]{2}\d{5}):(\d+)(?:-(\d+))?$/i);
    if (m) {
      var base = m[1].toUpperCase(), start = parseInt(m[2]), end = m[3] ? parseInt(m[3]) : start;
      var wanted = phelpsRange(base, start, end, 3);
      return {
        key: 'tablets', title: "Tablets of Bahá'u'lláh",
        label: base + " ¶" + start + (end > start ? '–' + end : ''),
        filter: function(e) { return wanted.indexOf(e.phelps) >= 0; }
      };
    }
    // esw:5 or esw:10-20 (Epistle to the Son of the Wolf)
    m = code.match(/^esw:(\d+)(?:-(\d+))?$/);
    if (m) {
      var start = parseInt(m[1]), end = m[2] ? parseInt(m[2]) : start;
      var wanted = phelpsRange('BH00005', start, end, 3);
      return {
        key: 'lawh', title: "Other Tablets",
        label: "ESW ¶" + start + (end > start ? '–' + end : ''),
        filter: function(e) { return wanted.indexOf(e.phelps) >= 0; }
      };
    }
    // tdp:tablet:para or tdp:tablet:start-end (Tablets of the Divine Plan)
    var tdpBases = {1:'AB00956',2:'AB01505',3:'AB01130',4:'AB00936',5:'AB01552',6:'AB00218',7:'AB00049',8:'AB00032',9:'AB00241',10:'AB00209',11:'AB00184',12:'AB00210',13:'AB00169',14:'AB00094'};
    m = code.match(/^tdp:(\d+):(\d+)(?:-(\d+))?$/);
    if (m) {
      var tablet = parseInt(m[1]), start = parseInt(m[2]), end = m[3] ? parseInt(m[3]) : start;
      var base = tdpBases[tablet];
      if (base) {
        var wanted = phelpsRange(base, start, end, 3);
        return {
          key: 'divineplan', title: "Tablets of the Divine Plan",
          label: "TDP Tablet " + tablet + " ¶" + start + (end > start ? '–' + end : ''),
          filter: function(e) { return wanted.indexOf(e.phelps) >= 0; }
        };
      }
    }
    // tdp:tablet (whole tablet)
    m = code.match(/^tdp:(\d+)$/);
    if (m) {
      var tablet = parseInt(m[1]);
      var base = tdpBases[tablet];
      if (base) {
        return {
          key: 'divineplan', title: "Tablets of the Divine Plan",
          label: "TDP Tablet " + tablet,
          filter: function(e) { return e.phelps && e.phelps.startsWith(base); }
        };
      }
    }
    // gpb:fw:para or gpb:rp:para (Foreword / Reference Points)
    m = code.match(/^gpb:(fw|rp):(\d+)(?:-(\d+))?$/i);
    if (m) {
      var chCode = 'SEBKGPB' + m[1].toUpperCase();
      var start = parseInt(m[2]), end = m[3] ? parseInt(m[3]) : start;
      var label = m[1].toUpperCase();
      return {
        key: 'gpb', title: "God Passes By",
        label: "GPB " + label + " ¶" + start + (end > start ? '–' + end : ''),
        filter: function(e) { return e.phelps === chCode && e.order >= start && e.order <= end; }
      };
    }
    // gpb:fw or gpb:rp (whole Foreword / Reference Points)
    m = code.match(/^gpb:(fw|rp)$/i);
    if (m) {
      var chCode = 'SEBKGPB' + m[1].toUpperCase();
      var label = m[1].toUpperCase();
      return {
        key: 'gpb', title: "God Passes By",
        label: "GPB " + label,
        filter: function(e) { return e.phelps === chCode; }
      };
    }
    // gpb:chapter:para or gpb:chapter:start-end (God Passes By paragraphs)
    m = code.match(/^gpb:(\d+):(\d+)(?:-(\d+))?$/);
    if (m) {
      var ch = parseInt(m[1]), start = parseInt(m[2]), end = m[3] ? parseInt(m[3]) : start;
      var chCode = 'SEBKGPB:' + ch;
      return {
        key: 'gpb', title: "God Passes By",
        label: "GPB " + ch + ":" + start + (end > start ? '–' + end : ''),
        filter: function(e) { return e.phelps === chCode && e.order >= start && e.order <= end; }
      };
    }
    // gpb:chapter or gpb:start-end (whole chapters)
    m = code.match(/^gpb:(\d+)(?:-(\d+))?$/);
    if (m) {
      var start = parseInt(m[1]), end = m[2] ? parseInt(m[2]) : start;
      var codes = [];
      for (var c = start; c <= end; c++) codes.push('SEBKGPB:' + c);
      return {
        key: 'gpb', title: "God Passes By",
        label: "GPB Ch " + start + (end > start ? '–' + end : ''),
        filter: function(e) { return codes.indexOf(e.phelps) >= 0; }
      };
    }
    // gems:para or gems:start-end (Gems of Divine Mysteries — single base BH00012)
    m = code.match(/^gems:(\d+)(?:-(\d+))?$/);
    if (m) {
      var start = parseInt(m[1]), end = m[2] ? parseInt(m[2]) : start;
      var wanted = phelpsRange('BH00012', start, end, 4);
      return {
        key: 'gems', title: "Gems of Divine Mysteries",
        label: "Gems ¶" + start + (end > start ? '–' + end : ''),
        filter: function(e) { return wanted.indexOf(e.phelps) >= 0; }
      };
    }
    // swb:BB#####:para or swb:BB#####:start-end (Selections from the Writings of the Báb)
    m = code.match(/^swb:(BB\d{5}):(\d+)(?:-(\d+))?$/i);
    if (m) {
      var base = m[1].toUpperCase(), start = parseInt(m[2]), end = m[3] ? parseInt(m[3]) : start;
      var wanted = phelpsRange(base, start, end, 4);
      return {
        key: 'swb', title: "Selections from the Writings of the Báb",
        label: "SWB " + base + " ¶" + start + (end > start ? '–' + end : ''),
        filter: function(e) { return wanted.indexOf(e.phelps) >= 0; }
      };
    }
    // swb:BB##### (whole tablet)
    m = code.match(/^swb:(BB\d{5})$/i);
    if (m) {
      var base = m[1].toUpperCase();
      return {
        key: 'swb', title: "Selections from the Writings of the Báb",
        label: "SWB " + base,
        filter: function(e) { return e.phelps && e.phelps.startsWith(base); }
      };
    }
    // swab:AB#####:para — Selections from the Writings of 'Abdu'l-Bahá
    m = code.match(/^swab:(AB\d{5}):(\d+)(?:-(\d+))?$/i);
    if (m) {
      var base = m[1].toUpperCase(), start = parseInt(m[2]), end = m[3] ? parseInt(m[3]) : start;
      var wanted = phelpsRange(base, start, end, 4);
      return {
        key: 'swab', title: "Selections from the Writings of ‘Abdu’l-Bahá",
        label: "SWAB " + base + " ¶" + start + (end > start ? '–' + end : ''),
        filter: function(e) { return wanted.indexOf(e.phelps) >= 0; }
      };
    }
    m = code.match(/^swab:(AB\d{5})$/i);
    if (m) {
      var base = m[1].toUpperCase();
      return {
        key: 'swab', title: "Selections from the Writings of ‘Abdu’l-Bahá",
        label: "SWAB " + base,
        filter: function(e) { return e.phelps && e.phelps.startsWith(base); }
      };
    }
    // pup:ABU####:para — Promulgation of Universal Peace
    m = code.match(/^pup:(ABU\d{4}):(\d+)(?:-(\d+))?$/i);
    if (m) {
      var base = m[1].toUpperCase(), start = parseInt(m[2]), end = m[3] ? parseInt(m[3]) : start;
      var wanted = phelpsRange(base, start, end, 4);
      return {
        key: 'pup', title: "The Promulgation of Universal Peace",
        label: "PUP " + base + " ¶" + start + (end > start ? '–' + end : ''),
        filter: function(e) { return wanted.indexOf(e.phelps) >= 0; }
      };
    }
    m = code.match(/^pup:(ABU\d{4})$/i);
    if (m) {
      var base = m[1].toUpperCase();
      return {
        key: 'pup', title: "The Promulgation of Universal Peace",
        label: "PUP " + base,
        filter: function(e) { return e.phelps && e.phelps.startsWith(base); }
      };
    }
    return null;
  }

  // ── The list view/editor ───────────────────────────────────────────
  function create(cfg) {
    const container = cfg.container;
    const onChange = cfg.onChange || function () {};
    const getCompareLangs = cfg.getCompareLangs || function () { return []; };

    async function render() {
      // removable may be a function (devotional toggles shared → builder mode)
      const removable = typeof cfg.removable === 'function'
        ? cfg.removable() : cfg.removable !== false;
      const codes = cfg.getItems();
      const lang = cfg.getLang();
      const allLanguages = cfg.getAllLanguages();
      const compareLangs = getCompareLangs();
      const rtlLang = allLanguages.find(l => l.code === lang); const rtl = rtlLang ? rtlLang.rtl : false;

      if (codes.length === 0) {
        container.innerHTML = '';
        if (cfg.emptyEl) cfg.emptyEl.style.display = '';
        return;
      }
      if (cfg.emptyEl) cfg.emptyEl.style.display = 'none';

      container.innerHTML = '<p style="color:var(--text-secondary)">Loading...</p>';

      const items = [];
      for (const entry of codes) {
        // Version-locked items resolve to their pinned UUID first; if that
        // fails (deleted/corrupt), fall back to normal resolution and mark
        // the item as having a broken pin so we can warn the user.
        let resolved = entry.v ? await resolveVersionLocked(entry) : null;
        const vBroken = entry.v && !resolved; // pin was set but didn't resolve
        let useLang = entry.lang || lang;
        if (resolved && resolved._verLang) useLang = resolved._verLang;
        if (!resolved) resolved = await resolveCode(entry.code, useLang);
        const item = { code: entry.code, lang: entry.lang, v: entry.v, useLang, resolved, vBroken };
        // Pre-fetch comparison languages (resolveCode handles -translit internally)
        // Comparison ignores v-lock — it always shows latest in each compare lang.
        if (compareLangs.length > 0) {
          item._compareCache = {};
          for (const cl of compareLangs) {
            if (cl !== useLang) {
              item._compareCache[cl] = await resolveCode(entry.code, cl);
            }
          }
        }
        items.push(item);
      }

      container.innerHTML = '';
      const STAR = '\u{1F7D9}'; // nine-pointed star
      items.forEach((item, idx) => {
        // Star separator between items
        if (idx > 0) {
          const sep = document.createElement('div');
          sep.className = 'dev-star-sep';
          sep.textContent = STAR;
          container.appendChild(sep);
        }

        const div = document.createElement('div');
        div.className = 'dev-item';
        div.dataset.idx = String(idx);

        const r = item.resolved;
        const textHtml = r ? renderMd(r.text) : '<span class="dev-item-na">Not available in ' + item.useLang + '</span>';
        const metaText = r ? (r.category || '') + (r.name ? ' — ' + r.name : '') : '';
        const meta = r && r.link ? '<a href="' + r.link + '" style="color:inherit;text-decoration:none;border-bottom:1px dotted var(--text-secondary)">' + metaText + '</a>' : metaText;
        const isRtlLang = allLanguages.find(l => l.code === item.useLang); const isRtl = isRtlLang && isRtlLang.rtl;
        // Author from PIN prefix. Prayers (r.type === 'prayer') get a signature
        // line under the text; non-prayer writings get an inline italic
        // attribution in the meta header.
        const localAuthor = authorFromPin(item.code, item.useLang);
        const localSig = sigFromPin(item.code, item.useLang);
        const isPrayer = r && r.type === 'prayer';
        const authorSigHtml = (isPrayer && localSig)
          ? '<div class="prayer-author"' + (isRtl ? ' dir="rtl"' : '') + '>' + escapeHtml(localSig) + '</div>'
          : '';
        const writingAttrHtml = (!isPrayer && localAuthor)
          ? '<span class="writing-attribution"> ' + escapeHtml(formatAttribution(localAuthor)) + '</span>'
          : '';
        const lockLabel = item.lang ? item.lang.toUpperCase() : '\u{1F310}';
        const lockTitle = item.lang ? 'Locked to ' + item.lang + ' (click to unlock)' : 'Using global language (click to lock to ' + lang + ')';
        // Version-lock button: only meaningful for prayers (not bible/quran/writings)
        const isPrayerType = r && r.type === 'prayer';
        const vLockLabel = item.v ? (item.vBroken ? '⚠️' : '\u{1F4CC}') : 'v';
        const vLockTitle = item.v
          ? (item.vBroken
              ? 'Pinned version no longer resolvable — showing latest. Click to unpin and restore normal behaviour.'
              : 'Pinned to specific translation (click to unpin)')
          : (isPrayerType && r && r.version
              ? 'Pin this exact translation (current version)'
              : 'No specific version to pin');
        // Permalink: prefer the pinned version, else the currently-shown
        // translation's version. Empty for non-prayers (Bible/Quran/writings
        // have their own ref-based deep links via the meta).
        const permalinkV = item.v || (isPrayerType && r ? (r.v || r.version) : '');
        const permalinkHtml = permalinkV
          ? `<a class="dev-item-permalink" href="/p/?v=${permalinkV}" title="Permalink to this exact translation (share or save)">\u{1F517}</a>`
          : '';

        // Build comparison columns if in comparison mode
        let columnsHtml = '';
        if (compareLangs.length > 0) {
          const colLangs = [item.useLang].concat(compareLangs);
          const ncols = colLangs.length;
          columnsHtml = '<div class="dev-item-columns" style="grid-template-columns:repeat(' + ncols + ',1fr)">';
          for (const cl of colLangs) {
            {
              const colResolved = (cl === item.useLang) ? r : (item._compareCache && item._compareCache[cl]);
              const isTranslit = isTranslitCode(cl);
              const tEntry = isTranslit ? TRANSLIT_ENTRIES.find(function(te) { return te.code === cl; }) : null;
              const colRtlInfo = !isTranslit ? allLanguages.find(l => l.code === cl) : null;
              const colRtl = colRtlInfo && colRtlInfo.rtl;
              const colName = tEntry ? tEntry.name : (colRtlInfo ? colRtlInfo.name : cl.toUpperCase());
              const colText = colResolved ? renderMd(colResolved.text) : '<span class="dev-item-na">' + cl + '</span>';
              const colClass = isTranslit ? ' translit-col' : '';
              // Per-column author signature in the column's own language.
              const colAuthor = sigFromPin(item.code, cl);
              const colAuthorHtml = (isPrayer && colAuthor)
                ? '<div class="prayer-author"' + (colRtl ? ' dir="rtl"' : '') + '>' + escapeHtml(colAuthor) + '</div>'
                : '';
              columnsHtml += '<div class="dev-item-col' + colClass + '"' + (colRtl ? ' dir="rtl"' : '') + '><div class="dev-item-col-header">' + colName + '</div>' + colText + colAuthorHtml + '</div>';
            }
          }
          columnsHtml += '</div>';
        }

        div.innerHTML = `
          <div class="dev-item-header no-print">
            <span class="dev-item-drag" title="Drag to reorder">&#x2630;</span>
            <span class="dev-item-code">${item.code}</span>
            <span class="dev-item-meta">${meta}${writingAttrHtml}</span>
            <button class="btn-expand dev-item-expand" title="Full screen">&#x26F6;</button>
            <button class="dev-item-lock${item.lang ? ' locked' : ''}" title="${lockTitle}">${lockLabel}</button>
            ${(isPrayerType || item.v) ? `<button class="dev-item-vlock${item.v ? (item.vBroken ? ' locked broken' : ' locked') : ''}"${(!item.v && (!r || !r.version)) ? ' disabled' : ''} title="${vLockTitle}">${vLockLabel}</button>` : ''}
            ${permalinkHtml}
            ${removable ? '<button class="dev-item-remove" title="Remove">&times;</button>' : ''}
          </div>
          ${compareLangs.length > 0 ? columnsHtml : '<div class="dev-item-text"' + (isRtl ? ' dir="rtl"' : '') + '>' + textHtml + '</div>'}
          ${authorSigHtml}
          <div class="dev-item-pin">${item.code}</div>`;

        // Lock/unlock language button
        const lockBtn = div.querySelector('.dev-item-lock');
        if (lockBtn) {
          lockBtn.addEventListener('click', () => {
            if (codes[idx].lang) {
              delete codes[idx].lang; // unlock
            } else {
              codes[idx].lang = lang; // lock to current global language
            }
            onChange(codes);
            render();
          });
        }

        // Lock/unlock version button (pin a specific translation by UUID)
        const vLockBtn = /** @type {HTMLButtonElement} */ (div.querySelector('.dev-item-vlock'));
        if (vLockBtn && !vLockBtn.disabled) {
          vLockBtn.addEventListener('click', () => {
            if (codes[idx].v) {
              delete codes[idx].v; // unpin
            } else if (r && r.version && window.uuidToBase36) {
              codes[idx].v = window.uuidToBase36(r.version);
            }
            onChange(codes);
            render();
          });
        }

        // Remove button
        const removeBtn = div.querySelector('.dev-item-remove');
        if (removeBtn) {
          removeBtn.addEventListener('click', () => {
            codes.splice(idx, 1);
            onChange(codes);
            render();
          });
        }

        // Full screen expand — works with both single text and comparison columns
        var expandBtn = div.querySelector('.dev-item-expand');
        if (expandBtn) {
          expandBtn.addEventListener('click', function() {
            var columnsEl = div.querySelector('.dev-item-columns');
            var textEl = div.querySelector('.dev-item-text');
            if (!columnsEl && !textEl) return;
            var overlay = document.createElement('div');
            overlay.className = 'expanded-overlay' + (columnsEl ? ' expanded-overlay-cols' : '');
            var metaHtml = div.querySelector('.dev-item-meta') ? div.querySelector('.dev-item-meta').innerHTML : '';
            var contentHtml = '';
            if (columnsEl) {
              var ncols = columnsEl.querySelectorAll('.dev-item-col').length;
              contentHtml = '<div class="dev-item-columns dev-item-columns-scroll expand-body" ' +
                'style="grid-template-columns:repeat(' + ncols + ',minmax(360px,1fr))">';
              columnsEl.querySelectorAll('.dev-item-col').forEach(function(/** @type {HTMLElement} */ col) {
                contentHtml += '<div class="' + col.className + '"' + (col.dir ? ' dir="' + col.dir + '"' : '') +
                  ' style="max-height:none;overflow:visible">' + col.innerHTML + '</div>';
              });
              contentHtml += '</div>';
            } else {
              var sig = textEl.nextElementSibling;
              var sigHtml = (sig && sig.classList && sig.classList.contains('prayer-author'))
                ? sig.outerHTML : '';
              contentHtml = '<div class="expand-body"' + (isRtl ? ' dir="rtl"' : '') + '>' + textEl.innerHTML + sigHtml + '</div>';
            }
            var zoomBar = '<div class="expand-zoom">' +
              '<button class="expand-zoom-btn" data-zoom="-">A−</button>' +
              '<button class="expand-zoom-btn" data-zoom="0">A</button>' +
              '<button class="expand-zoom-btn" data-zoom="+">A+</button>' +
              '</div>';
            overlay.innerHTML = '<button class="expand-close">✖</button>' + zoomBar +
              (metaHtml ? '<div style="color:var(--text-secondary);font-size:.85rem;margin-bottom:1rem">' + metaHtml + '</div>' : '') +
              contentHtml;
            document.body.appendChild(overlay);
            var body = /** @type {HTMLElement} */ (overlay.querySelector('.expand-body'));
            var zoom = parseFloat(localStorage.getItem('dev_expand_zoom') || '1');
            var applyZoom = function() { if (body) body.style.fontSize = zoom + 'em'; };
            applyZoom();
            overlay.querySelectorAll('.expand-zoom-btn').forEach(function(b) {
              b.addEventListener('click', function() {
                var op = b.getAttribute('data-zoom');
                if (op === '+') zoom = Math.min(2.5, zoom + 0.15);
                else if (op === '-') zoom = Math.max(0.7, zoom - 0.15);
                else zoom = 1;
                localStorage.setItem('dev_expand_zoom', String(zoom));
                applyZoom();
              });
            });
            overlay.querySelector('.expand-close').addEventListener('click', function() { overlay.remove(); });
            var escHandler = function(e) { if (e.key === 'Escape') { overlay.remove(); document.removeEventListener('keydown', escHandler); } };
            document.addEventListener('keydown', escHandler);
          });
        }

        // Drag-and-drop reordering — only from the drag handle
        var dragHandle = /** @type {HTMLElement} */ (div.querySelector('.dev-item-drag'));
        dragHandle.addEventListener('mousedown', function() { div.draggable = true; });
        dragHandle.addEventListener('touchstart', function() { div.draggable = true; }, {passive: true});
        div.addEventListener('dragstart', e => {
          div.classList.add('dragging');
          e.dataTransfer.effectAllowed = 'move';
          e.dataTransfer.setData('text/plain', idx.toString());
        });
        div.addEventListener('dragend', () => { div.classList.remove('dragging'); div.draggable = false; });
        div.addEventListener('dragover', e => { e.preventDefault(); div.classList.add('drag-over'); });
        div.addEventListener('dragleave', () => div.classList.remove('drag-over'));
        div.addEventListener('drop', e => {
          e.preventDefault();
          div.classList.remove('drag-over');
          const fromIdx = parseInt(e.dataTransfer.getData('text/plain'));
          const toIdx = idx;
          if (fromIdx !== toIdx) {
            const [moved] = codes.splice(fromIdx, 1);
            codes.splice(toIdx, 0, moved);
            onChange(codes);
            render();
          }
        });

        container.appendChild(div);
      });
    }

    return { render: render };
  }

  // ── Public API ─────────────────────────────────────────────────────
  window.hwProgramView = {
    create: create,
    resolveCode: resolveCode,
    getPrayerMap: getPrayerMap,
    getWritingMap: getWritingMap,
    getVersionIndex: getVersionIndex,
    resolveVersionLocked: resolveVersionLocked,
    parseWritingShorthand: parseWritingShorthand,
    authorFromPin: authorFromPin,
    sigFromPin: sigFromPin,
    formatAttribution: formatAttribution,
    TRANSLIT_ENTRIES: TRANSLIT_ENTRIES,
    isTranslitCode: isTranslitCode,
  };
})();
