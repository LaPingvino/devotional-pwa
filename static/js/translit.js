// Arabic/Persian → Latin transliteration reading aid with vowel prediction
(function() {
  // Letter mappings (Bahá'í-style transliteration with acute accents)
  var L = {
    // Arabic
    '\u0621':'\'','\u0627':'a','\u0623':'\'','\u0625':'\'','\u0622':'\u00E1',
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

  // Lighter normalization for KNOWN table — preserves hamza/alif variants
  // to avoid collisions like إن (inna) vs آن (ān) vs أن (anna)
  function normalizeKnownKey(word) {
    var s = stripTashkeel(word);
    return s.replace(/\u0643/g, '\u06A9')
            .replace(/\u064A/g, '\u06CC')
            .replace(/\u0649/g, '\u06CC')
            .replace(/\u0640/g, '');
    // NOTE: does NOT normalize hamza variants
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
        // Only plain alif and alif-madda are carrier letters (silent)
        // أ (hamza-above) and إ (hamza-below) are consonants (glottal stop)
        var isAlif = (c === '\u0627' || c === '\u0622');
        var isWaw = (c === '\u0648');
        var isYa = (c === '\u064A' || c === '\u06CC');
        i++;
        // Check if waw/ya act as long vowels
        // Trigger when: preceded by matching vowel, OR preceded by consonant with no vowel
        // (implied kasra/damma). Only if they do NOT have their own following vowel mark.
        var nextIsTashkeel = (i < n && V[chars[i]] !== undefined && chars[i] !== '\u0652');
        if (isWaw && (prevVowel === 'u' || (prevVowel === '' && prevBase)) && !nextIsTashkeel) {
          // و as long ú — remove preceding 'u' if present
          if (cur.length > 0 && cur[cur.length - 1] === 'u') {
            cur = cur.slice(0, -1);
          } else if (result.length > 0 && result[result.length - 1].t === 'u') {
            result.pop();
          }
          cur += '\u00FA'; // ú
          prevBase = ''; prevVowel = '';
          continue;
        }
        if (isYa && (prevVowel === 'i' || (prevVowel === '' && prevBase)) && !nextIsTashkeel) {
          // ي as long í — remove preceding 'i' if present
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

  // Digraphs that represent single sounds — don't break these
  var DIGRAPHS = ['sh','th','dh','kh','ch','zh','gh'];

  function breakClusters(segs, origChars, origIndices) {
    // Flatten to get raw consonant/vowel structure
    var flat = '';
    for (var i = 0; i < segs.length; i++) flat += segs[i].t;
    if (!flat) return segs;

    // First pass: convert likely long vowel patterns
    // 'w' between consonants or at end is likely ú
    // 'y' between consonants or at end is likely í
    flat = flat.replace(/([bcdfghjklmnpqrstvxz\u1E00-\u1EFF\u02BB])w(?=[bcdfghjklmnpqrstvxyz\u1E00-\u1EFF\u02BB]|$)/g, '$1\u00FA');
    flat = flat.replace(/([bcdfghjklmnpqrstvxz\u1E00-\u1EFF\u02BB])y(?=[bcdfghjklmnpqrstvxyz\u1E00-\u1EFF\u02BB]|$)/g, '$1\u00ED');

    // Tokenize into sound units (digraphs count as 1 unit)
    var units = [];
    var j = 0;
    while (j < flat.length) {
      var di = flat.substring(j, j + 2);
      if (DIGRAPHS.indexOf(di) >= 0) {
        units.push(di);
        j += 2;
      } else {
        units.push(flat[j]);
        j++;
      }
    }

    // Break consonant clusters using bigram vowel predictions
    // Detect if text is likely Persian (has Persian-specific letters)
    var isPersian = origChars && origChars.some(function(c) {
      return '\u067E\u0686\u0698\u06AF'.indexOf(c) >= 0;
    });
    var bg = bigramData ? (isPersian ? bigramData.fa : bigramData.ar) : null;

    // Map unit indices to original Arabic letter indices
    // Each non-breaker unit corresponds to one Arabic letter (digraphs = 1 letter)
    var unitToOrig = [];
    var origIdx = 0;
    for (var ui = 0; ui < units.length; ui++) {
      var uu = units[ui];
      if (uu.length === 1 && (isVowelChar(uu) || uu === '-' || uu === ' ' || uu === '\'' || uu === '\u02BB')) {
        unitToOrig.push(-1);
      } else {
        unitToOrig.push(origIdx);
        origIdx++;
      }
    }

    var out = [];
    var consRun = 0;
    var isBreaker = function(u) { return u.length === 1 && (isVowelChar(u) || u === '-' || u === ' ' || u === '\'' || u === '\u02BB'); };
    for (var k = 0; k < units.length; k++) {
      var u = units[k];
      if (isBreaker(u)) {
        consRun = 0;
        out.push({t: u, p: false});
      } else {
        consRun++;
        out.push({t: u, p: false});
        var atStart = (consRun === 1 && (k === 0 || (k > 0 && isBreaker(units[k - 1]))));
        if ((consRun >= 2 || atStart) && k + 1 < units.length) {
          var next = units[k + 1];
          if (next && !isBreaker(next)) {
            // Use bigram prediction if available, otherwise 'a'
            var vowel = 'a';
            if (bg && origChars) {
              var oi1 = unitToOrig[k], oi2 = unitToOrig[k + 1];
              if (oi1 >= 0 && oi2 >= 0 && oi1 < origChars.length && oi2 < origChars.length) {
                var bgKey = origChars[oi1] + origChars[oi2];
                if (bg[bgKey]) vowel = bg[bgKey];
                else if (atStart) {
                  var initKey = '^' + origChars[oi1];
                  if (bg[initKey]) vowel = bg[initKey];
                }
              }
            }
            out.push({t: vowel, p: true});
            consRun = 0;
          }
        }
      }
    }
    return out;
  }

  // Known Bahá'í terms → correct transliteration (bypasses heuristics)
  // Keys are normalized (stripped tashkeel, Persian letter variants)
  var KNOWN = {};
  (function() {
    var terms = {
      'الله': 'All\u00E1h', 'اللّه': 'All\u00E1h',
      'بسمالله': 'Bismi\'ll\u00E1h',
      'هوالله': 'Huva\'ll\u00E1h', 'هواللّه': 'Huva\'ll\u00E1h',
      'هوالابهی': 'Huva\'l-Abh\u00E1', 'هوالابهى': 'Huva\'l-Abh\u00E1',
      'بهاءالله': 'Bah\u00E1\'u\'ll\u00E1h',
      'عبدالبهاء': '\'Abdu\'l-Bah\u00E1',
      'الابهی': 'al-Abh\u00E1', 'الابهى': 'al-Abh\u00E1',
      'ابهی': 'Abh\u00E1', 'ابهى': 'Abh\u00E1',
      'الرحمن': 'ar-Ra\u1E25m\u00E1n',
      'الرحیم': 'ar-Ra\u1E25\u00EDm',
      'الاعظم': 'al-A\'\u1E93am',
      'الاعلی': 'al-A\'l\u00E1', 'الاعلى': 'al-A\'l\u00E1',
      'الاقدس': 'al-Aqdas',
      'الایقان': 'al-\u00CDq\u00E1n',
      'ایقان': '\u00CDq\u00E1n',
      'البهاء': 'al-Bah\u00E1\'',
      'البیان': 'al-Bay\u00E1n',
      'بیان': 'Bay\u00E1n',
      'الکتاب': 'al-Kit\u00E1b',
      'کتاب': 'Kit\u00E1b',
      'القدوس': 'al-Qudd\u00FAs',
      'سبحان': 'Sub\u1E25\u00E1n',
      'الحمد': 'al-\u1E24amd',
      'محمد': 'Mu\u1E25ammad',
      'بهاء': 'Bah\u00E1\'',
      'عبد': '\'Abd',
      'حسین': '\u1E24usayn',
      'حسن': '\u1E24asan',
      'علی': '\'Al\u00ED',
      'فاطمه': 'F\u00E1\u1E6Dimih',
      'قران': 'Qur\'\u00E1n',
      'انجیل': 'Inj\u00EDl',
      'تورات': 'Tawr\u00E1t',
      'الهی': 'Il\u00E1h\u00ED',
      'الاهی': 'Il\u00E1h\u00ED',
      'ربی': 'Rabb\u00ED',
      'یا': 'Y\u00E1',
      // Short ambiguous words — only unvocalized defaults
      // (vocalized text uses tashkeel directly)
      'ان': 'inna', // unpointed default
      'إنک': 'innaka', 'انک': 'innaka',
      'إنه': 'innahu', 'انه': 'innahu',
      'إنا': 'inn\u00E1', 'انا': 'inn\u00E1',
      // على ('alá, preposition) vs علی ('Alí, name)
      'على': '\u02BBal\u00E1',
      'علی': '\u02BBAl\u00ED',
      // Common Arabic particles/prepositions
      'ما': 'm\u00E1',
      'من': 'min',
      'هذا': 'h\u00E1dh\u00E1',
      'هذه': 'h\u00E1dhihi',
      'ذلک': 'dh\u00E1lika',
      'کل': 'kull',
      'کلّ': 'kull',
      'بعد': 'ba\'d',
      'قبل': 'qabl',
      'حتی': '\u1E25att\u00E1',
      'بین': 'bayn',
      'عند': '\'ind',
      'فی': 'f\u00ED',
      'الذی': 'alladh\u00ED',
      'الّذی': 'alladh\u00ED',
      'التی': 'allat\u00ED',
      'الّتی': 'allat\u00ED',
      'الذین': 'alladh\u00EDn',
      'هو': 'huwa',
      'هی': 'hiya',
      'قد': 'qad',
      'ثم': 'thumm',
      'ثمّ': 'thumm',
      'أو': 'aw',
      'لم': 'lam',
      'لن': 'lan',
      'إذا': 'idh\u00E1',
      'اذا': 'idh\u00E1',
      'كان': 'k\u00E1na',
      'کان': 'k\u00E1na',
      'ذلک': 'dh\u00E1lika',
      'ذلك': 'dh\u00E1lika',
      'أنت': 'anta',
      'أنتم': 'antum',
      // More common terms
      'اکبر': 'Akbar', 'اكبر': 'Akbar',
      'بسمالله': 'Bismi\'ll\u00E1h',
      'بهاءالابهی': 'Bah\u00E1\'u\'l-Abh\u00E1',
      'بهاءالابهى': 'Bah\u00E1\'u\'l-Abh\u00E1',
      'یابهاءالابهی': 'Y\u00E1 Bah\u00E1\'u\'l-Abh\u00E1',
      'یابهاءالابهى': 'Y\u00E1 Bah\u00E1\'u\'l-Abh\u00E1',
      'العالمین': 'al-\'\u00C1lam\u00EDn',
      'العالمين': 'al-\'\u00C1lam\u00EDn',
      'لله': 'li\'ll\u00E1h',
      'رب': 'Rabb',
      'ربک': 'Rabbika',
      'لا': 'l\u00E1',
      'اله': 'il\u00E1h',
      'الا': 'ill\u00E1',
      'اسالک': 'as\'aluka',
      'بامرک': 'bi-amrika',
      'سبحانک': 'Sub\u1E25\u00E1nak',
      'الملک': 'al-Mulk',
      'الحکیم': 'al-\u1E24ak\u00EDm',
      'العزیز': 'al-\'Az\u00EDz',
      'المقتدر': 'al-Muqtadir',
      'القدیر': 'al-Qad\u00EDr',
      'الغفور': 'al-Ghaf\u00FAr',
      'الکریم': 'al-Kar\u00EDm',
      'العظیم': 'al-\'A\u1E93\u00EDm',
      'العلیم': 'al-\'Al\u00EDm',
      'الحق': 'al-\u1E24aqq',
      'البهائی': 'al-Bah\u00E1\'\u00ED',
      'البهائیه': 'al-Bah\u00E1\'\u00EDyyih',
      'خدا': 'Khud\u00E1',
      'پروردگار': 'Parvardig\u00E1r',
      // Common Persian words
      'ای': 'Ay', 'این': '\u00EDn', 'آن': '\u00E1n',
      'است': 'ast', 'نیست': 'n\u00EDst',
      'در': 'dar', 'بر': 'bar', 'هر': 'har',
      'شو': 'shav', 'شد': 'shud', 'شده': 'shudih',
      'بود': 'b\u00FAd', 'باشد': 'b\u00E1shad',
      'کرد': 'kard', 'گفت': 'guft',
      'خود': 'khud', 'چون': 'ch\u00FAn', 'چه': 'chih',
      'اگر': 'agar', 'ولی': 'val\u00ED',
      'باید': 'b\u00E1yad', 'شاید': 'sh\u00E1yad',
      'یعنی': 'ya\'n\u00ED', 'زیرا': 'z\u00EDr\u00E1',
      'بلکه': 'balkih', 'اول': 'avval',
      'اوّل': 'avval', 'دیگر': 'd\u00EDgar',
      'عالم': '\'\u00E1lam', 'علم': '\'ilm',
      'قدم': 'qadam', 'بردار': 'bard\u00E1r',
      'گذار': 'gudh\u00E1r', 'پاک': 'p\u00E1k',
      'فقر': 'faqr', 'غنا': 'ghin\u00E1',
      'بقا': 'baq\u00E1', 'فنا': 'fan\u00E1',
      'حبّ': '\u1E25ubb', 'ربّ': 'rabb',
      'مسکین': 'misk\u00EDn', 'مسکینی': 'misk\u00EDn\u00ED',
      'غنایت': 'ghin\u00E1yat',
      'سلطان': 'sul\u1E6D\u00E1n',
      'ملکوت': 'malak\u00FAt',
      'وحدانیت': 'va\u1E25d\u00E1n\u00EDyyat',
      'فردانیت': 'fard\u00E1n\u00EDyyat',
      'بحر': 'ba\u1E25r', 'شهود': 'shuh\u00FAd',
      'غیب': 'ghayb', 'مالک': 'm\u00E1lik',
      'گواهی': 'guv\u00E1h\u00ED',
      'فرزند': 'farzand',
      'آلایش': '\u00E1l\u00E1yish',
      'آسایش': '\u00E1s\u00E1yish',
      'افلاک': 'afl\u00E1k',
      'کمال': 'kam\u00E1l',
      'تعالی': 'ta\'\u00E1l\u00E1',
      'شأن': 'sha\'n', 'شأنه': 'sha\'nih',
      'العظمه': 'al-\'A\u1E93amih',
      'الاقتدار': 'al-Iqtid\u00E1r',
    };
    for (var k in terms) KNOWN[normalizeKnownKey(k)] = terms[k];
  })();

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
        // Check known-word map (always — curated entries are reliable)
        var knownKey = normalizeKnownKey(part);
        if (KNOWN[knownKey]) {
          allSegs.push({t: KNOWN[knownKey], p: false});
          continue;
        }
        var hasVowelMarks = /[\u064E\u064F\u0650\u064B\u064C\u064D\u0670]/.test(part);
        var predicted = false;
        var toTranslit = part;
        // If no vowel marks, try dictionary lookup (even if it has shadda/sukun)
        if (!hasVowelMarks && vowelDict) {
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
        } else if (hasVowelMarks) {
          predicted = false;
        }
        var chars = Array.from(toTranslit);
        var segs = transliterateSegment(chars, predicted);
        // If no dictionary match and no vowel marks, break consonant clusters
        if (!predicted && !hasVowelMarks) {
          // Build mapping from Arabic letters to transliteration output indices
          var origLetters = Array.from(part).filter(function(c) {
            return L[c] !== undefined;
          });
          // origIndices maps unit index → index into origLetters
          // (approximate: unit count roughly matches letter count for non-digraph letters)
          segs = breakClusters(segs, origLetters, null);
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
    // Fix al at word start → al- (definite article)
    // Only match plain 'a' (not 'á' from alif-madda آ) to avoid breaking آلایش etc.
    // Only after space/start (not after ' which could be ayn)
    s = s.replace(/(^|[ \n])al(?=[a-z\u00E1\u00ED\u00FA\u02BB\u1E00-\u1EFF])/g, '$1al-');
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
    // Liaison: word ending in vowel + space + Alláh → 'lláh (Arabic waṣl)
    s = s.replace(/([aeiou\u00E1\u00ED\u00FA]) All\u00E1h/g, '$1\'ll\u00E1h');
    // Liaison: word ending in consonant + i + space + al-X → 'l-X (genitive construct)
    s = s.replace(/i (al-)/g, 'i\'$1');
    // Word-initial hamza: 'a/'i at word start → a/i (hamza is silent initially)
    s = s.replace(/(^|[ \n])'([aiu])/g, '$1$2');
    // Also after al- prefix: al'a → al-a (hamza after definite article)
    s = s.replace(/al'([aiu\u00E1\u00ED\u00FA])/g, 'al-$1');
    // But mid-word glottal stop after wa- prefix stays: wa'anta (not waanta)
    // Capitalize start of sentences (after . ! ? or start of string)
    s = s.replace(/(^|[.!?]\s*)([a-z\u00E1\u00ED\u00FA\u1E00-\u1EFF\u02BB])/g, function(m, pre, ch) {
      return pre + ch.toUpperCase();
    });
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
  var bigramData = null; // consonant-pair vowel predictions
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
    Promise.all([
      origFetchFn('/data/ar_vowels.json').then(function(r) { return r.json(); }),
      origFetchFn('/data/ar_bigrams.json').then(function(r) { return r.json(); }).catch(function() { return null; })
    ]).then(function(results) {
      vowelDict = results[0];
      bigramData = results[1];
      dictLoading = false;
      cb();
    })
      .catch(function() { vowelDict = {}; dictLoading = false; cb(); });
  }

  // Add transliteration annotations within a root element.
  // Returns array of inserted annotation elements.
  function addTranslitIn(root) {
    var walker = document.createTreeWalker(
      root,
      NodeFilter.SHOW_ELEMENT,
      { acceptNode: function(node) {
        if (node.closest('.site-header, .sidebar, script, style, .translit-line, .nav-dropdown-menu, .ui-lang-menu, .search-lang-dropdown')) return NodeFilter.FILTER_REJECT;
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
      // Inside prayer card headers: replace text inline (restore on remove)
      var inHeader = el.closest('.prayer-card-header');
      if (inHeader) {
        el.setAttribute('data-translit-orig', el.innerHTML);
        el.innerHTML = r.html;
        el.style.direction = 'ltr';
        el.style.fontStyle = 'italic';
        el.classList.add('translit-replaced');
        added.push(el);
      } else {
        var line = document.createElement('div');
        line.className = 'translit-line';
        line.innerHTML = r.html;
        line.style.cssText = 'font-size:.8em;color:var(--text-secondary);font-style:italic;direction:ltr;text-align:left;margin-top:2px;line-height:1.4;';
        el.parentNode.insertBefore(line, el.nextSibling);
        added.push(line);
      }
    });
    return added;
  }

  function addTranslit() {
    removeTranslit();
    annotations = addTranslitIn(document.body);
    active = true;
  }

  // Expose for dynamic content (search table re-renders)
  window.__translitActive = function() { return active; };
  window.__addTranslitIn = function(root) { annotations = annotations.concat(addTranslitIn(root)); };

  function removeTranslit() {
    annotations.forEach(function(el) {
      if (el.classList && el.classList.contains('translit-replaced')) {
        // Restore original content for inline-replaced elements
        var orig = el.getAttribute('data-translit-orig');
        if (orig !== null) {
          el.innerHTML = orig;
          el.removeAttribute('data-translit-orig');
          el.style.direction = '';
          el.style.fontStyle = '';
          el.classList.remove('translit-replaced');
        }
      } else if (el.parentNode) {
        el.parentNode.removeChild(el);
      }
    });
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

  // Expose transliterate for use by other components (e.g. comparison view)
  window.__transliterate = function(text) {
    if (!vowelDict) {
      // Synchronous fallback: transliterate without dictionary
      return transliterate(text);
    }
    return transliterate(text);
  };
  window.__transliterateWithDict = function(text, cb) {
    loadDict(function() { cb(transliterate(text)); });
  };

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
