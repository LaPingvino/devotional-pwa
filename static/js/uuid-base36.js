// UUID ↔ base36 conversion. 128 bits of UUID → ~25 char base36 string,
// vs 36 chars in canonical UUID format (saves ~30% in URLs and storage).
// Used for compact permalinks (/p/?v=<base36>) and version-locked devotional
// program items.
(function () {
  // BigInt-based to handle full 128-bit precision; falls back to canonical
  // UUID strings if BigInt is unavailable (very old browsers).
  function uuidToBase36(uuid) {
    if (typeof BigInt === 'undefined') return uuid;
    if (!uuid) return '';
    var hex = uuid.replace(/-/g, '');
    if (!/^[0-9a-fA-F]{32}$/.test(hex)) return uuid; // not a UUID — return as-is
    var n = BigInt('0x' + hex);
    return n.toString(36);
  }

  function base36ToUuid(b36) {
    if (typeof BigInt === 'undefined') return b36;
    if (!b36) return '';
    if (b36.length === 36 && b36.indexOf('-') >= 0) return b36; // already a UUID
    if (!/^[0-9a-z]+$/.test(b36)) return ''; // invalid base36
    var n;
    try { n = BigInt(parseInt(0)); n = BigInt(0); } catch { return ''; }
    var chars = '0123456789abcdefghijklmnopqrstuvwxyz';
    for (var i = 0; i < b36.length; i++) {
      var v = chars.indexOf(b36[i]);
      if (v < 0) return '';
      n = n * 36n + BigInt(v);
    }
    var hex = n.toString(16);
    while (hex.length < 32) hex = '0' + hex;
    if (hex.length > 32) return ''; // overflow
    return hex.slice(0, 8) + '-' + hex.slice(8, 12) + '-' + hex.slice(12, 16) +
           '-' + hex.slice(16, 20) + '-' + hex.slice(20, 32);
  }

  window.uuidToBase36 = uuidToBase36;
  window.base36ToUuid = base36ToUuid;
})();
