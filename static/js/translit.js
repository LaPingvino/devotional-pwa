// Arabic/Persian → Latin transliteration reading aid with vowel prediction
(function() {
  // Letter mappings (academic transliteration with diacritics)
  var L = {
    // Arabic
    '\u0621':'\'','\u0627':'\u0101','\u0623':'a','\u0625':'i','\u0622':'\u0101',
    '\u0628':'b','\u062A':'t','\u062B':'th','\u062C':'j','\u062D':'\u1E25',
    '\u062E':'kh','\u062F':'d','\u0630':'dh','\u0631':'r','\u0632':'z',
    '\u0633':'s','\u0634':'sh','\u0635':'\u1E63','\u0636':'\u1E0D','\u0637':'\u1E6D',
    '\u0638':'\u1E93','\u0639':'\u02BB','\u063A':'gh','\u0641':'f','\u0642':'q',
    '\u0643':'k','\u0644':'l','\u0645':'m','\u0646':'n','\u0647':'h',
    '\u0648':'w','\u064A':'y','\u0649':'\u0101','\u0629':'h',
    '\u0626':'\'','\u0624':'\'',
    // Persian extras
    '\u067E':'p','\u0686':'ch','\u0698':'zh','\u06AF':'g',
    '\u06A9':'k', // Persian kaf
    '\u06CC':'y', // Persian yeh
    '\u0640':'', // tatweel (kashida)
  };

  // Tashkeel (vowel marks)
  var V = {
    '\u064E':'a','\u064F':'u','\u0650':'i',
    '\u064B':'an','\u064C':'un','\u064D':'in',
    '\u0651':'','\u0652':'','\u0670':'\u0101',
  };

  var TASHKEEL = '\u064B\u064C\u064D\u064E\u064F\u0650\u0651\u0652\u0670';

  function hasTashkeel(word) {
    for (var i = 0; i < word.length; i++) {
      if (TASHKEEL.indexOf(word[i]) >= 0) return true;
    }
    return false;
  }

  function stripTashkeel(word) {
    var out = '';
    for (var i = 0; i < word.length; i++) {
      if (TASHKEEL.indexOf(word[i]) < 0) out += word[i];
    }
    return out;
  }

  // Normalize Arabic/Persian letter variants for dictionary lookup
  function normalizeKey(word) {
    var s = stripTashkeel(word);
    return s.replace(/\u0643/g, '\u06A9')   // Arabic kaf → Persian kaf
            .replace(/\u064A/g, '\u06CC')   // Arabic yeh → Persian yeh
            .replace(/\u0649/g, '\u06CC')   // Alif maqsura → yeh
            .replace(/\u0640/g, '')          // Remove tatweel
            .replace(/[\u0623\u0625\u0622]/g, '\u0627'); // Hamza variants → plain alif
  }

  // Transliterate a single segment. Returns array of {t: text, p: bool} segments.
  // p=true means predicted (uncertain) vowels.
  function transliterateSegment(chars, predicted) {
    var result = [];
    var i = 0, n = chars.length;
    var cur = ''; // accumulate certain text
    function flush() { if (cur) { result.push({t: cur, p: false}); cur = ''; } }

    while (i < n) {
      var c = chars[i];
      // Pass through non-Arabic
      if (!L[c] && !V[c] && !(c >= '\u0620' && c <= '\u065F') && !(c >= '\u0670' && c <= '\u06FF')) {
        cur += c;
        i++;
        continue;
      }
      // Tashkeel without letter
      if (V[c] !== undefined && !L[c]) {
        if (V[c]) {
          if (predicted) { flush(); result.push({t: V[c], p: true}); }
          else cur += V[c];
        }
        i++;
        continue;
      }
      // Letter
      if (L[c] !== undefined) {
        var base = L[c];
        i++;
        // Shadda = double consonant
        if (i < n && chars[i] === '\u0651') {
          if (base.length > 0) base = base + base[base.length - 1];
          i++;
        }
        // Collect following vowels
        var vowel = '';
        while (i < n && V[chars[i]] !== undefined) {
          var v = chars[i];
          if (v === '\u0651') {
            if (base.length > 0) base = base + base[base.length - 1];
          } else if (v === '\u0652') {
            // sukun - no vowel
          } else if (V[v]) {
            vowel += V[v];
          }
          i++;
        }
        // Base consonant is always certain; vowel may be predicted
        cur += base;
        if (vowel) {
          if (predicted) { flush(); result.push({t: vowel, p: true}); }
          else cur += vowel;
        }
      } else {
        i++; // skip unknown Arabic-range char
      }
    }
    flush();
    return result;
  }

  // Main transliterate: returns {text: string, html: string}
  // html has <span class="vp"> around predicted vowels
  function transliterate(text) {
    // Split into Arabic words and non-Arabic segments
    var parts = text.match(/[\u0621-\u065F\u0670-\u06FF\u0640]+|[^\u0621-\u065F\u0670-\u06FF\u0640]+/g);
    if (!parts) return {text: '', html: ''};

    var allSegs = [];
    for (var pi = 0; pi < parts.length; pi++) {
      var part = parts[pi];
      // Check if this part is Arabic
      if (/[\u0621-\u065F\u0670-\u06FF]/.test(part)) {
        var predicted = false;
        var toTranslit = part;
        // If no tashkeel, try dictionary lookup
        if (!hasTashkeel(part) && vowelDict) {
          var nk = normalizeKey(part);
          // Try direct lookup
          var found = vowelDict[nk];
          // Try without leading و/ب/ف/ل prefixes
          if (!found && nk.length > 2) {
            var pre = nk[0];
            if (pre === '\u0648' || pre === '\u0628' || pre === '\u0641' || pre === '\u0644') {
              var rest = nk.substring(1);
              found = vowelDict[rest];
              if (found) found = part[0] + found;
            }
          }
          if (found) {
            toTranslit = found;
            predicted = true;
          }
        } else if (hasTashkeel(part)) {
          predicted = false;
        }
        var chars = Array.from(toTranslit);
        var segs = transliterateSegment(chars, predicted);
        allSegs = allSegs.concat(segs);
      } else {
        // Non-Arabic: pass through
        allSegs.push({t: part, p: false});
      }
    }

    // Build plain text and HTML
    var plain = '';
    var html = '';
    for (var i = 0; i < allSegs.length; i++) {
      var s = allSegs[i];
      plain += s.t;
      if (s.p) {
        html += '<span class="vp">' + escHtml(s.t) + '</span>';
      } else {
        html += escHtml(s.t);
      }
    }

    // Clean up alif-lam
    plain = cleanAlif(plain);
    html = cleanAlif(html);
    return {text: plain, html: html};
  }

  function cleanAlif(s) {
    s = s.replace(/\b\u0101l-?/g, 'al-');
    s = s.replace(/a\u0101/g, '\u0101');
    s = s.replace(/\u0101\u0101/g, '\u0101');
    return s;
  }

  function escHtml(s) {
    return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  }

  // Check if text contains Arabic/Persian characters
  function hasArabic(text) {
    return /[\u0600-\u06FF\u0750-\u077F\uFB50-\uFDFF\uFE70-\uFEFF]/.test(text);
  }

  var active = false;
  var annotations = [];
  var vowelDict = null; // loaded lazily
  var dictLoading = false;

  function loadDict(cb) {
    if (vowelDict) return cb();
    if (dictLoading) {
      // Wait for ongoing load
      var check = setInterval(function() {
        if (vowelDict) { clearInterval(check); cb(); }
      }, 100);
      return;
    }
    dictLoading = true;
    var origFetchFn = window.__origFetch || window.fetch;
    origFetchFn('/data/ar_vowels.json')
      .then(function(r) { return r.json(); })
      .then(function(d) { vowelDict = d; dictLoading = false; cb(); })
      .catch(function() { vowelDict = {}; dictLoading = false; cb(); });
  }

  function addTranslit() {
    removeTranslit();
    // Find all text nodes with Arabic/Persian content
    var walker = document.createTreeWalker(
      document.body,
      NodeFilter.SHOW_ELEMENT,
      { acceptNode: function(node) {
        if (node.closest('.site-header, .sidebar, script, style, .translit-line, .nav-dropdown-menu, .ui-lang-menu')) return NodeFilter.FILTER_REJECT;
        var dir = node.getAttribute('dir');
        var text = node.textContent || '';
        if ((dir === 'rtl' || hasArabic(text)) && node.children.length === 0 && text.trim().length > 0) {
          return NodeFilter.FILTER_ACCEPT;
        }
        // Check block-level elements with direct Arabic text
        if (hasArabic(text) && (node.tagName === 'P' || node.tagName === 'TD' || node.tagName === 'LI' || node.tagName === 'DIV' || node.tagName === 'SPAN')) {
          for (var cn = node.firstChild; cn; cn = cn.nextSibling) {
            if (cn.nodeType === 3 && hasArabic(cn.textContent)) return NodeFilter.FILTER_ACCEPT;
          }
        }
        return NodeFilter.FILTER_SKIP;
      }}
    );

    var elements = [];
    var node;
    while (node = walker.nextNode()) elements.push(node);

    elements.forEach(function(el) {
      var text = el.textContent.trim();
      if (!text || !hasArabic(text)) return;
      var r = transliterate(text);
      if (!r.text.trim() || r.text === text) return;
      var line = document.createElement('div');
      line.className = 'translit-line';
      line.innerHTML = r.html;
      line.style.cssText = 'font-size:.8em;color:var(--text-secondary);font-style:italic;direction:ltr;text-align:left;margin-top:2px;line-height:1.4;';
      el.parentNode.insertBefore(line, el.nextSibling);
      annotations.push(line);
    });
    active = true;
  }

  function removeTranslit() {
    annotations.forEach(function(el) { if (el.parentNode) el.parentNode.removeChild(el); });
    annotations = [];
    active = false;
  }

  function toggle() {
    if (active) {
      removeTranslit();
      var btn = document.getElementById('translit-btn');
      if (btn) btn.classList.toggle('active', false);
    } else {
      loadDict(function() {
        addTranslit();
        var btn = document.getElementById('translit-btn');
        if (btn) btn.classList.toggle('active', true);
      });
    }
  }

  // Add button to header
  var btn = document.createElement('button');
  btn.id = 'translit-btn';
  btn.className = 'translit-toggle';
  btn.title = 'Toggle transliteration (Arabic/Persian)';
  btn.textContent = '\u0628b';
  btn.style.display = 'none';
  btn.addEventListener('click', toggle);

  // Insert into header
  var header = document.querySelector('.site-header');
  var langPicker = document.querySelector('.ui-lang-picker');
  if (header && langPicker) {
    header.insertBefore(btn, langPicker);
  }

  // Show button after a short delay (content may load dynamically)
  function checkAndShow() {
    if (hasArabic(document.body.textContent || '')) {
      btn.style.display = '';
    }
  }
  checkAndShow();
  setTimeout(checkAndShow, 2000);
  // Also check after any fetch-driven content loads
  var origFetch = window.fetch;
  if (origFetch) {
    window.__origFetch = origFetch; // save for dict loading
    window.fetch = function() {
      return origFetch.apply(this, arguments).then(function(r) {
        setTimeout(checkAndShow, 500);
        return r;
      });
    };
  }
})();
