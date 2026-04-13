// Arabic/Persian → Latin transliteration reading aid
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

  function transliterate(text) {
    var result = [];
    var chars = Array.from(text);
    var i = 0, n = chars.length;
    while (i < n) {
      var c = chars[i];
      // Pass through non-Arabic
      if (!L[c] && !V[c] && !(c >= '\u0620' && c <= '\u065F') && !(c >= '\u0670' && c <= '\u06FF')) {
        result.push(c);
        i++;
        continue;
      }
      // Tashkeel without letter
      if (V[c] !== undefined && !L[c]) {
        if (V[c]) result.push(V[c]);
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
        result.push(base + vowel);
      } else {
        i++; // skip unknown Arabic-range char
      }
    }
    var out = result.join('');
    // Clean up alif-lam
    out = out.replace(/\b\u0101l-?/g, 'al-');
    // Double-alif
    out = out.replace(/a\u0101/g, '\u0101');
    out = out.replace(/\u0101\u0101/g, '\u0101');
    return out;
  }

  // Check if text contains Arabic/Persian characters
  function hasArabic(text) {
    return /[\u0600-\u06FF\u0750-\u077F\uFB50-\uFDFF\uFE70-\uFEFF]/.test(text);
  }

  var active = false;
  var annotations = [];

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
          // Only if it has direct text content (not just children)
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
      var t = transliterate(text);
      if (!t.trim() || t === text) return;
      var line = document.createElement('div');
      line.className = 'translit-line';
      line.textContent = t;
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
    if (active) removeTranslit();
    else addTranslit();
    var btn = document.getElementById('translit-btn');
    if (btn) btn.classList.toggle('active', active);
  }

  // Only show button if page has Arabic/Persian content
  if (hasArabic(document.body.textContent || '')) {
    var btn = document.createElement('button');
    btn.id = 'translit-btn';
    btn.className = 'translit-toggle';
    btn.title = 'Toggle transliteration';
    btn.textContent = 'Aa';
    btn.addEventListener('click', toggle);
    // Insert near the language picker
    var header = document.querySelector('.site-header');
    var langPicker = document.querySelector('.ui-lang-picker');
    if (header && langPicker) {
      header.insertBefore(btn, langPicker);
    }
  }
})();
