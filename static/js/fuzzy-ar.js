// Arabic/Persian fuzzy text normalization for search matching.
// Strips diacritics, normalizes letter variants, removes kashida.
(function () {
  // Tashkeel (diacritics) range: U+064B–U+065F, plus U+0670 (superscript alef)
  var tashkeel = /[\u064B-\u065F\u0670\u06D6-\u06DC\u06DF-\u06E4\u06E7\u06E8\u06EA-\u06ED]/g;
  // Kashida / tatweel
  var kashida = /\u0640/g;

  // Letter variant mapping
  var map = {
    '\u0622': '\u0627', // آ → ا
    '\u0623': '\u0627', // أ → ا
    '\u0625': '\u0627', // إ → ا
    '\u0671': '\u0627', // ٱ → ا
    '\u0629': '\u0647', // ة → ه
    '\u0643': '\u06A9', // ك → ک
    '\u064A': '\u06CC', // ي → ی
    '\u0624': '\u0648', // ؤ → و
    '\u0626': '\u06CC', // ئ → ی
  };
  var mapRe = new RegExp('[' + Object.keys(map).join('') + ']', 'g');

  window.normalizeAr = function (s) {
    return s
      .replace(tashkeel, '')
      .replace(kashida, '')
      .replace(mapRe, function (c) { return map[c]; });
  };
})();
