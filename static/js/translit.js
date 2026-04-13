// Arabic/Persian → Latin transliteration reading aid with vowel prediction
(function() {
  // Letter mappings (Bahá'í-style transliteration with acute accents)
  var L = {
    // Arabic
    '\u0621':'\'','\u0627':'\u00E1','\u0623':'a','\u0625':'i','\u0622':'\u00E1',
    '\u0628':'b','\u062A':'t','\u062B':'th','\u062C':'j','\u062D':'\u1E25',
    '\u062E':'kh','\u062F':'d','\u0630':'dh','\u0631':'r','\u0632':'z',
    '\u0633':'s','\u0634':'sh','\u0635':'\u1E63','\u0636':'\u1E0D','\u0637':'\u1E6D',
    '\u0638':'\u1E93','\u0639':'\u02BB','\u063A':'gh','\u0641':'f','\u0642':'q',
    '\u0643':'k','\u0644':'l','\u0645':'m','\u0646':'n','\u0647':'h',
    '\u0648':'w','\u064A':'y','\u0649':'\u00E1','\u0629':'h',
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
    '\u0651':'','\u0652':'','\u0670':'\u00E1',
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
    var prevBase = ''; // track previous consonant for shadda dedup
    var prevVowel = ''; // track previous vowel for long vowel detection
    function flush() { if (cur) { result.push({t: cur, p: false}); cur = ''; } }

    while (i < n) {
      var c = chars[i];
      // Pass through non-Arabic
      if (!L[c] && !V[c] && !(c >= '\u0620' && c <= '\u065F') && !(c >= '\u0670' && c <= '\u06FF')) {
        cur += c;
        prevBase = '';
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
        var isAlif = (c === '\u0627' || c === '\u0623' || c === '\u0625' || c === '\u0622');
        var isWaw = (c === '\u0648');
        var isYa = (c === '\u064A' || c === '\u06CC');
        i++;
        // Check if waw/ya act as long vowels (preceded by matching short vowel)
        // Only if they do NOT have their own following vowel mark (then they're consonants)
        var nextIsTashkeel = (i < n && V[chars[i]] !== undefined && chars[i] !== '\u0652');
        if (isWaw && prevVowel === 'u' && !nextIsTashkeel) {
          // و after damma without own vowel = long ú
          if (cur.length > 0 && cur[cur.length - 1] === 'u') {
            cur = cur.slice(0, -1);
          } else if (result.length > 0 && result[result.length - 1].t === 'u') {
            result.pop();
          }
          cur += '\u00FA'; // ú
          prevBase = ''; prevVowel = '';
          continue;
        }
        if (isYa && prevVowel === 'i' && !nextIsTashkeel) {
          // ي after kasra without own vowel = long í
          if (cur.length > 0 && cur[cur.length - 1] === 'i') {
            cur = cur.slice(0, -1);
          } else if (result.length > 0 && result[result.length - 1].t === 'i') {
            result.pop();
          }
          cur += '\u00ED'; // í
          prevBase = ''; prevVowel = '';
          continue;
        }
        // Shadda = double consonant (but not if previous letter was same — already doubled)
        if (i < n && chars[i] === '\u0651') {
          if (base.length > 0 && base !== prevBase) {
            base = base + base[base.length - 1];
          }
          i++;
        }
        // Collect following vowels
        var vowel = '';
        var hasSukun = false;
        while (i < n && V[chars[i]] !== undefined) {
          var v = chars[i];
          if (v === '\u0651') {
            if (base.length > 0 && base !== prevBase) {
              base = base + base[base.length - 1];
            }
          } else if (v === '\u0652') {
            hasSukun = true;
          } else if (V[v]) {
            vowel += V[v];
          }
          i++;
        }
        // Alif is a carrier letter: if followed by a short vowel, use the vowel alone
        if (isAlif && vowel && vowel !== '\u00E1') {
          prevBase = base;
          prevVowel = vowel;
          if (predicted) { flush(); result.push({t: vowel, p: true}); }
          else cur += vowel;
        } else {
          // Alif after fatha = á (long a)
          if (isAlif && prevVowel === 'a') {
            if (cur.length > 0 && cur[cur.length - 1] === 'a') {
              cur = cur.slice(0, -1);
            } else if (result.length > 0 && result[result.length - 1].t === 'a') {
              result.pop();
            }
            cur += '\u00E1';
            prevBase = base; prevVowel = '';
          } else {
            cur += base;
            prevBase = base;
            prevVowel = vowel || '';
            if (vowel) {
              if (predicted) { flush(); result.push({t: vowel, p: true}); }
              else cur += vowel;
            }
          }
        }
      } else {
        i++; // skip unknown Arabic-range char
      }
    }
    flush();
    return result;
  }

  // Break consonant clusters in unvocalized words: insert predicted 'a' when
  // 3+ consonant letters appear without any vowel between them.
  // Long vowels (ā, ū, ī) and 'al-' prefix don't count as clusters.
  var VOWELS_SET = 'aeiou\u00E1\u00ED\u00FA';
  function isVowelChar(ch) { return VOWELS_SET.indexOf(ch) >= 0; }

  function breakClusters(segs) {
    // Flatten to get raw consonant/vowel structure, then re-split
    var flat = '';
    for (var i = 0; i < segs.length; i++) flat += segs[i].t;
    if (!flat) return segs;

    // First pass: convert likely long vowel patterns
    // 'w' between consonants or at end is likely ú
    // 'y' between consonants or at end is likely í
    flat = flat.replace(/([bcdfghjklmnpqrstvxz\u1E00-\u1EFF\u02BB])w(?=[bcdfghjklmnpqrstvxyz\u1E00-\u1EFF\u02BB]|$)/g, '$1\u00FA');
    flat = flat.replace(/([bcdfghjklmnpqrstvxz\u1E00-\u1EFF\u02BB])y(?=[bcdfghjklmnpqrstvxyz\u1E00-\u1EFF\u02BB]|$)/g, '$1\u00ED');

    // Second pass: break remaining consonant clusters with predicted 'a'
    // Arabic doesn't allow initial or 3+ consonant clusters, so insert vowels
    var out = [];
    var consRun = 0;
    var isBreaker = function(ch) { return isVowelChar(ch) || ch === '-' || ch === ' ' || ch === '\'' || ch === '\u02BB'; };
    for (var j = 0; j < flat.length; j++) {
      var ch = flat[j];
      if (isBreaker(ch)) {
        consRun = 0;
        out.push({t: ch, p: false});
      } else {
        consRun++;
        out.push({t: ch, p: false});
        // Insert 'a' when: 2+ consonants are followed by another consonant,
        // OR 1 consonant at word start followed by consonant (no initial clusters in Arabic)
        var atStart = (consRun === 1 && (j === 0 || (j > 0 && isBreaker(flat[j - 1]))));
        if ((consRun >= 2 || atStart) && j + 1 < flat.length) {
          var next = flat[j + 1];
          if (next && !isBreaker(next)) {
            out.push({t: 'a', p: true});
            consRun = 0;
          }
        }
      }
    }
    return out;
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
          // Try compound splits (هوالله → هو + الله, etc.)
          if (!found && nk.length > 3) {
            // Try هو prefix (common invocation prefix)
            if (nk.substring(0, 2) === '\u0647\u0648') {
              var afterHu = nk.substring(2);
              var huRest = vowelDict[afterHu];
              if (huRest) found = '\u0647\u064F\u0648\u064E' + huRest; // هُوَ + rest
            }
          }
          // Try without leading و/ب/ف/ل/ک prefixes
          if (!found && nk.length > 2) {
            var pre = nk[0];
            if (pre === '\u0648' || pre === '\u0628' || pre === '\u0641' || pre === '\u0644' || pre === '\u06A9') {
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
        // If no dictionary match and no tashkeel, break consonant clusters
        // by inserting predicted 'a' between 3+ consecutive consonants
        if (!predicted && !hasTashkeel(part)) {
          segs = breakClusters(segs);
        }
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
    plain = cleanOutput(plain);
    html = cleanOutput(html);
    return {text: plain, html: html};
  }

  function cleanOutput(s) {
    // Fix á followed by 'l' → 'al-' (definite article), anywhere in the string
    s = s.replace(/\u00E1l(?=[a-z\u00E1\u00ED\u00FA\u02BB\u1E00-\u1EFF])/g, 'al-');
    // Sun letter assimilation: al-XX → aX-X (consume doubled consonant from shadda)
    s = s.replace(/al-([tdrzsnl\u1E63\u1E0D\u1E6D\u1E93\u1E25])\1*/g, function(m, c) {
      return 'a' + c + '-' + c;
    });
    s = s.replace(/al-(sh|th|dh)\1*/g, function(m, c) {
      return 'a' + c + '-' + c;
    });
    // Fix triple+ consonant runs from shadda-after-duplicate (lll→ll, rrr→rr, etc.)
    s = s.replace(/([bcdfghjklmnpqrstvwxyz\u1E00-\u1EFF\u02BB])\1{2,}/g, '$1$1');
    // Double-alif cleanup
    s = s.replace(/a\u00E1/g, '\u00E1');
    s = s.replace(/\u00E1\u00E1/g, '\u00E1');
    // Strip trailing grammatical case vowels on words (the -u, -i endings)
    s = s.replace(/([bcdfghjklmnpqrstvwxyz\u1E00-\u1EFF])([ui])(?=[ \n,.]|$)/g, '$1');
    // Strip tanwin endings (-an, -un, -in) at word end
    s = s.replace(/(an|un|in)(?=[ \n,.]|$)/g, '');
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

  // Add transliteration annotations within a root element.
  // Returns array of inserted annotation elements.
  function addTranslitIn(root) {
    var walker = document.createTreeWalker(
      root,
      NodeFilter.SHOW_ELEMENT,
      { acceptNode: function(node) {
        if (node.closest('.site-header, .sidebar, script, style, .translit-line, .nav-dropdown-menu, .ui-lang-menu')) return NodeFilter.FILTER_REJECT;
        var dir = node.getAttribute('dir');
        var text = node.textContent || '';
        if ((dir === 'rtl' || hasArabic(text)) && node.children.length === 0 && text.trim().length > 0) {
          return NodeFilter.FILTER_ACCEPT;
        }
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
    var added = [];

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
      added.push(line);
    });
    return added;
  }

  function addTranslit() {
    removeTranslit();
    annotations = addTranslitIn(document.body);
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

  // Watch for fullscreen overlays and inject a translit toggle button
  var observer = new MutationObserver(function(mutations) {
    mutations.forEach(function(m) {
      m.addedNodes.forEach(function(node) {
        if (node.nodeType !== 1 || !node.classList.contains('expanded-overlay')) return;
        if (!hasArabic(node.textContent || '')) return;
        // Add translit button next to close button
        var overlayBtn = document.createElement('button');
        overlayBtn.className = 'translit-toggle overlay-translit';
        overlayBtn.textContent = '\u0628b';
        overlayBtn.title = 'Toggle transliteration';
        var overlayAnnotations = [];
        var overlayActive = false;
        overlayBtn.addEventListener('click', function() {
          if (overlayActive) {
            overlayAnnotations.forEach(function(el) { if (el.parentNode) el.parentNode.removeChild(el); });
            overlayAnnotations = [];
            overlayActive = false;
            overlayBtn.classList.remove('active');
          } else {
            loadDict(function() {
              overlayAnnotations = addTranslitIn(node);
              overlayActive = true;
              overlayBtn.classList.add('active');
            });
          }
        });
        // Insert after close button
        var closeBtn = node.querySelector('.expand-close');
        if (closeBtn) {
          closeBtn.parentNode.insertBefore(overlayBtn, closeBtn.nextSibling);
        } else {
          node.insertBefore(overlayBtn, node.firstChild);
        }
        // If main translit is active, auto-enable in overlay too
        if (active) {
          loadDict(function() {
            overlayAnnotations = addTranslitIn(node);
            overlayActive = true;
            overlayBtn.classList.add('active');
          });
        }
      });
    });
  });
  observer.observe(document.body, { childList: true });

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
