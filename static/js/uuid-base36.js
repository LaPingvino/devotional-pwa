// UUID ↔ base36 conversion. 128 bits of UUID → ~25 char base36 string,
// vs 36 chars in canonical UUID format (saves ~30% in URLs and storage).
// Used for compact permalinks (/p/?v=<base36>) and version-locked devotional
// program items.
(function () {
  var CHARS = '0123456789abcdefghijklmnopqrstuvwxyz';

  // Pure ES5 128-bit byte-array base conversion (no BigInt literal/runtime needed,
  // so compact /p/?v=<base36> permalinks work on legacy browsers like iOS 9.3.5 too).
  function uuidToBase36(uuid) {
    if (!uuid) return '';
    var hex = uuid.replace(/-/g, '');
    if (!/^[0-9a-fA-F]{32}$/.test(hex)) return uuid; // not a UUID — return as-is
    var bytes = [];
    var i;
    for (i = 0; i < 32; i += 2) {
      bytes.push(parseInt(hex.slice(i, i + 2), 16));
    }
    var digits = [];
    while (bytes.length > 0) {
      var rem = 0;
      var next = [];
      for (i = 0; i < bytes.length; i++) {
        var cur = rem * 256 + bytes[i];
        var q = Math.floor(cur / 36);
        rem = cur % 36;
        if (next.length > 0 || q > 0) next.push(q);
      }
      digits.push(CHARS.charAt(rem));
      bytes = next;
    }
    if (digits.length === 0) return '0';
    return digits.reverse().join('');
  }

  function base36ToUuid(b36) {
    if (!b36) return '';
    if (b36.length === 36 && b36.indexOf('-') >= 0) return b36; // already a UUID
    // base36 is case-insensitive: 0-9 + a-z. Normalise to lowercase so callers
    // can paste UPPERCASE forms (QR readers, hand-typed URLs) and we still
    // round-trip cleanly.
    b36 = b36.toLowerCase();
    if (!/^[0-9a-z]+$/.test(b36)) return ''; // invalid base36
    var bytes = [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0];
    var i, j;
    for (i = 0; i < b36.length; i++) {
      var carry = CHARS.indexOf(b36.charAt(i));
      if (carry < 0) return '';
      for (j = 15; j >= 0; j--) {
        var cur = bytes[j] * 36 + carry;
        bytes[j] = cur & 255;
        carry = cur >>> 8;
      }
      if (carry > 0) return ''; // overflow (> 128 bits)
    }
    var hex = '';
    for (i = 0; i < 16; i++) {
      hex += (bytes[i] < 16 ? '0' : '') + bytes[i].toString(16);
    }
    return hex.slice(0, 8) + '-' + hex.slice(8, 12) + '-' + hex.slice(12, 16) +
           '-' + hex.slice(16, 20) + '-' + hex.slice(20, 32);
  }

  window.uuidToBase36 = uuidToBase36;
  window.base36ToUuid = base36ToUuid;
})();
