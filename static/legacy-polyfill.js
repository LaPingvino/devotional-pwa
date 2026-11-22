/* Legacy browser polyfill + transparent WebSQL offline cache for iOS 9-10 / Safari 9.
 * Strict ES5 only. Feature-gated so modern browsers incur zero overhead.
 */
(function () {
  'use strict';

  // 1. NodeList & HTMLCollection .forEach (Safari 9 lacks both)
  if (typeof NodeList !== 'undefined' && NodeList.prototype && !NodeList.prototype.forEach) {
    NodeList.prototype.forEach = Array.prototype.forEach;
  }
  if (typeof HTMLCollection !== 'undefined' && HTMLCollection.prototype && !HTMLCollection.prototype.forEach) {
    HTMLCollection.prototype.forEach = Array.prototype.forEach;
  }

  // 2. Object.values / Object.entries / Object.fromEntries / Object.assign
  if (!Object.values) {
    Object.values = function (obj) {
      var res = [], keys = Object.keys(obj);
      for (var i = 0; i < keys.length; i++) res.push(obj[keys[i]]);
      return res;
    };
  }
  if (!Object.entries) {
    Object.entries = function (obj) {
      var res = [], keys = Object.keys(obj);
      for (var i = 0; i < keys.length; i++) res.push([keys[i], obj[keys[i]]]);
      return res;
    };
  }
  if (!Object.fromEntries) {
    Object.fromEntries = function (entries) {
      var obj = {};
      if (!entries) return obj;
      for (var i = 0; i < entries.length; i++) {
        if (entries[i] && entries[i].length >= 2) obj[entries[i][0]] = entries[i][1];
      }
      return obj;
    };
  }
  if (!Object.assign) {
    Object.assign = function (target) {
      if (target == null) throw new TypeError('Cannot convert undefined or null to object');
      var to = Object(target);
      for (var i = 1; i < arguments.length; i++) {
        var src = arguments[i];
        if (src != null) {
          for (var k in src) {
            if (Object.prototype.hasOwnProperty.call(src, k)) to[k] = src[k];
          }
        }
      }
      return to;
    };
  }

  // 3. Array & String helpers
  if (!Array.prototype.includes) {
    Array.prototype.includes = function (el, from) {
      return this.indexOf(el, from || 0) !== -1;
    };
  }
  if (!Array.prototype.flat) {
    Array.prototype.flat = function (depth) {
      var d = depth === undefined ? 1 : Number(depth);
      var out = [];
      for (var i = 0; i < this.length; i++) {
        if (Array.isArray(this[i]) && d > 0) {
          out = out.concat(this[i].flat(d - 1));
        } else {
          out.push(this[i]);
        }
      }
      return out;
    };
  }
  if (!Array.prototype.flatMap) {
    Array.prototype.flatMap = function (fn, thisArg) {
      return this.map(fn, thisArg).flat(1);
    };
  }
  if (!String.prototype.includes) {
    String.prototype.includes = function (s, pos) {
      return this.indexOf(s, pos || 0) !== -1;
    };
  }
  if (!String.prototype.startsWith) {
    String.prototype.startsWith = function (s, pos) {
      var p = pos > 0 ? pos | 0 : 0;
      return this.substring(p, p + s.length) === s;
    };
  }
  if (!String.prototype.endsWith) {
    String.prototype.endsWith = function (s, len) {
      var l = (len === undefined || len > this.length) ? this.length : len;
      return this.substring(l - s.length, l) === s;
    };
  }
  if (!String.prototype.padStart) {
    String.prototype.padStart = function (len, pad) {
      var str = String(this);
      var p = pad === undefined ? ' ' : String(pad);
      if (!p) return str;
      while (str.length < len) str = p + str;
      return str.length > len ? str.slice(str.length - len) : str;
    };
  }
  if (!String.prototype.padEnd) {
    String.prototype.padEnd = function (len, pad) {
      var str = String(this);
      var p = pad === undefined ? ' ' : String(pad);
      if (!p) return str;
      while (str.length < len) str += p;
      return str.length > len ? str.slice(0, len) : str;
    };
  }
  if (!String.prototype.replaceAll) {
    String.prototype.replaceAll = function (search, replacement) {
      if (search instanceof RegExp) return this.replace(search, replacement);
      return this.split(String(search)).join(String(replacement));
    };
  }

  // 4. DOM helpers: matches, closest, remove
  if (typeof Element !== 'undefined') {
    if (!Element.prototype.matches) {
      Element.prototype.matches =
        Element.prototype.webkitMatchesSelector ||
        Element.prototype.msMatchesSelector ||
        function (s) {
          var matches = (this.document || this.ownerDocument).querySelectorAll(s);
          var i = matches.length;
          while (--i >= 0 && matches.item(i) !== this) {}
          return i > -1;
        };
    }
    if (!Element.prototype.closest) {
      Element.prototype.closest = function (s) {
        var el = this;
        while (el && el.nodeType === 1) {
          if (el.matches(s)) return el;
          el = el.parentElement || el.parentNode;
        }
        return null;
      };
    }
    if (!Element.prototype.remove) {
      Element.prototype.remove = function () {
        if (this.parentNode) this.parentNode.removeChild(this);
      };
    }
    // Safe scrollIntoView for Safari 9 (rejects options object or misbehaves)
    if (document.documentElement && !('scrollBehavior' in document.documentElement.style)) {
      var origScrollIntoView = Element.prototype.scrollIntoView;
      if (origScrollIntoView) {
        Element.prototype.scrollIntoView = function (arg) {
          if (arg && typeof arg === 'object') {
            return origScrollIntoView.call(this, arg.block !== 'end');
          }
          return origScrollIntoView.apply(this, arguments);
        };
      }
    }
  }

  // 5. URLSearchParams (Safari 9 lacks it)
  if (typeof window.URLSearchParams === 'undefined') {
    var USP = function (init) {
      this._entries = [];
      if (!init) return;
      if (typeof init === 'string') {
        var q = init.charAt(0) === '?' ? init.slice(1) : init;
        if (!q) return;
        var pairs = q.split('&');
        for (var i = 0; i < pairs.length; i++) {
          if (!pairs[i]) continue;
          var idx = pairs[i].indexOf('=');
          var k = idx >= 0 ? pairs[i].slice(0, idx) : pairs[i];
          var v = idx >= 0 ? pairs[i].slice(idx + 1) : '';
          this._entries.push([decodeURIComponent(k.replace(/\+/g, ' ')), decodeURIComponent(v.replace(/\+/g, ' '))]);
        }
      } else if (typeof init === 'object') {
        var keys = Object.keys(init);
        for (var j = 0; j < keys.length; j++) {
          this._entries.push([keys[j], String(init[keys[j]])]);
        }
      }
    };
    USP.prototype.get = function (name) {
      for (var i = 0; i < this._entries.length; i++) {
        if (this._entries[i][0] === name) return this._entries[i][1];
      }
      return null;
    };
    USP.prototype.getAll = function (name) {
      var out = [];
      for (var i = 0; i < this._entries.length; i++) {
        if (this._entries[i][0] === name) out.push(this._entries[i][1]);
      }
      return out;
    };
    USP.prototype.has = function (name) {
      return this.get(name) !== null;
    };
    USP.prototype.set = function (name, val) {
      var found = false, out = [];
      for (var i = 0; i < this._entries.length; i++) {
        if (this._entries[i][0] === name) {
          if (!found) { out.push([name, String(val)]); found = true; }
        } else {
          out.push(this._entries[i]);
        }
      }
      if (!found) out.push([name, String(val)]);
      this._entries = out;
    };
    USP.prototype.append = function (name, val) {
      this._entries.push([name, String(val)]);
    };
    USP.prototype.delete = function (name) {
      var out = [];
      for (var i = 0; i < this._entries.length; i++) {
        if (this._entries[i][0] !== name) out.push(this._entries[i]);
      }
      this._entries = out;
    };
    USP.prototype.forEach = function (fn, thisArg) {
      for (var i = 0; i < this._entries.length; i++) {
        fn.call(thisArg, this._entries[i][1], this._entries[i][0], this);
      }
    };
    USP.prototype.toString = function () {
      var parts = [];
      for (var i = 0; i < this._entries.length; i++) {
        parts.push(encodeURIComponent(this._entries[i][0]) + '=' + encodeURIComponent(this._entries[i][1]));
      }
      return parts.join('&');
    };
    window.URLSearchParams = USP;
  }

  // 6. IntersectionObserver fallback (immediately reports observed elements visible)
  if (typeof window.IntersectionObserver === 'undefined') {
    window.IntersectionObserver = function (cb) {
      this._cb = cb;
    };
    window.IntersectionObserver.prototype.observe = function (el) {
      var cb = this._cb, self = this;
      setTimeout(function () {
        try {
          cb([{ isIntersecting: true, intersectionRatio: 1, target: el }], self);
        } catch (e) {}
      }, 0);
    };
    window.IntersectionObserver.prototype.unobserve = function () {};
    window.IntersectionObserver.prototype.disconnect = function () {};
  }

  // 7. navigator.clipboard fallback
  if (typeof navigator !== 'undefined' && !navigator.clipboard) {
    navigator.clipboard = {
      writeText: function (text) {
        return new Promise(function (resolve, reject) {
          try {
            var ta = document.createElement('textarea');
            ta.value = String(text);
            ta.setAttribute('readonly', '');
            ta.style.position = 'fixed';
            ta.style.left = '-9999px';
            document.body.appendChild(ta);
            ta.select();
            var ok = document.execCommand('copy');
            document.body.removeChild(ta);
            if (ok) resolve(); else reject(new Error('copy failed'));
          } catch (e) {
            reject(e);
          }
        });
      }
    };
  }

  // 8. fetch() polyfill (XHR) + transparent WebSQL offline cache for /data/*.json
  //    Only activates on legacy browsers without native fetch (e.g. iOS 9-10).
  if (typeof window.fetch === 'undefined') {
    var db = null;
    try {
      if (typeof window.openDatabase === 'function') {
        db = window.openDatabase('hw_offline_v1', '1.0', 'Holy Writings Offline', 50 * 1024 * 1024);
        db.transaction(function (tx) {
          tx.executeSql('CREATE TABLE IF NOT EXISTS kv (k TEXT PRIMARY KEY, v TEXT, ts INTEGER)');
        });
      }
    } catch (e) {
      db = null;
    }

    function dbGet(key, cb) {
      if (!db) { cb(null); return; }
      try {
        db.readTransaction(function (tx) {
          tx.executeSql('SELECT v FROM kv WHERE k = ?', [key], function (tx2, res) {
            cb(res.rows.length > 0 ? res.rows.item(0).v : null);
          }, function () { cb(null); });
        }, function () { cb(null); });
      } catch (e) {
        cb(null);
      }
    }

    function dbPut(key, val) {
      if (!db) return;
      try {
        db.transaction(function (tx) {
          tx.executeSql('INSERT OR REPLACE INTO kv (k, v, ts) VALUES (?, ?, ?)', [key, val, Date.now()]);
        });
      } catch (e) {}
    }

    function isCacheableDataUrl(url) {
      if (!url || typeof url !== 'string') return false;
      var path = url;
      if (path.indexOf(location.origin) === 0) path = path.slice(location.origin.length);
      var qIdx = path.indexOf('?');
      if (qIdx >= 0) path = path.slice(0, qIdx);
      return path.indexOf('/data/') === 0 && path.slice(-5) === '.json';
    }

    function normalizeCacheKey(url) {
      var path = url;
      if (path.indexOf(location.origin) === 0) path = path.slice(location.origin.length);
      var qIdx = path.indexOf('?');
      if (qIdx >= 0) path = path.slice(0, qIdx);
      return path;
    }

    function looksLikeJson(txt) {
      if (!txt) return false;
      var trimmed = txt.replace(/^\s+/, '');
      var ch = trimmed.charAt(0);
      return ch === '{' || ch === '[';
    }

    function makeResponse(status, statusText, bodyText, headersMap) {
      var h = headersMap || {};
      return {
        ok: status >= 200 && status < 300,
        status: status,
        statusText: statusText || '',
        headers: {
          get: function (name) {
            return h[String(name).toLowerCase()] || null;
          }
        },
        text: function () { return Promise.resolve(bodyText); },
        json: function () {
          return new Promise(function (resolve, reject) {
            try { resolve(JSON.parse(bodyText)); } catch (e) { reject(e); }
          });
        },
        clone: function () { return makeResponse(status, statusText, bodyText, h); }
      };
    }

    function xhrFetch(url, opts) {
      return new Promise(function (resolve, reject) {
        var xhr = new XMLHttpRequest();
        var method = (opts && opts.method) ? opts.method : 'GET';
        xhr.open(method, url, true);
        if (opts && opts.headers) {
          var hKeys = Object.keys(opts.headers);
          for (var i = 0; i < hKeys.length; i++) {
            xhr.setRequestHeader(hKeys[i], opts.headers[hKeys[i]]);
          }
        }
        xhr.onload = function () {
          var hMap = {};
          try {
            var raw = xhr.getAllResponseHeaders() || '';
            var lines = raw.split(/\r?\n/);
            for (var j = 0; j < lines.length; j++) {
              var idx = lines[j].indexOf(':');
              if (idx > 0) {
                hMap[lines[j].slice(0, idx).trim().toLowerCase()] = lines[j].slice(idx + 1).trim();
              }
            }
          } catch (e) {}
          resolve(makeResponse(xhr.status || 200, xhr.statusText, xhr.responseText, hMap));
        };
        xhr.onerror = function () { reject(new TypeError('Network request failed')); };
        xhr.ontimeout = function () { reject(new TypeError('Network request timed out')); };
        xhr.send(opts && opts.body ? opts.body : null);
      });
    }

    window.fetch = function (input, opts) {
      var url = typeof input === 'string' ? input : (input && input.url ? input.url : String(input));
      var method = (opts && opts.method) ? opts.method.toUpperCase() : 'GET';
      var cacheable = (method === 'GET') && isCacheableDataUrl(url);

      if (!cacheable) return xhrFetch(url, opts);

      var key = normalizeCacheKey(url);
      // Fast offline path if browser reports offline
      if (typeof navigator !== 'undefined' && navigator.onLine === false) {
        return new Promise(function (resolve, reject) {
          dbGet(key, function (cached) {
            if (cached) {
              resolve(makeResponse(200, 'OK', cached, { 'content-type': 'application/json' }));
            } else {
              reject(new TypeError('Offline and not cached: ' + key));
            }
          });
        });
      }

      // Network-first, save valid JSON to WebSQL, fall back to WebSQL on error
      return xhrFetch(url, opts).then(function (resp) {
        if (resp.ok) {
          return resp.text().then(function (txt) {
            if (looksLikeJson(txt)) dbPut(key, txt);
            return makeResponse(resp.status, resp.statusText, txt, { 'content-type': 'application/json' });
          });
        }
        return resp;
      }, function (err) {
        return new Promise(function (resolve, reject) {
          dbGet(key, function (cached) {
            if (cached) {
              resolve(makeResponse(200, 'OK', cached, { 'content-type': 'application/json' }));
            } else {
              reject(err);
            }
          });
        });
      });
    };

    window.__hwLegacyCache = {
      clear: function (cb) {
        if (!db) { if (cb) cb(); return; }
        db.transaction(function (tx) {
          tx.executeSql('DELETE FROM kv', [], function () { if (cb) cb(); });
        });
      },
      stats: function (cb) {
        if (!db) { if (cb) cb({ count: 0, bytes: 0 }); return; }
        db.readTransaction(function (tx) {
          tx.executeSql('SELECT COUNT(*) AS c, COALESCE(SUM(LENGTH(v)), 0) AS b FROM kv', [], function (tx2, res) {
            var row = res.rows.item(0);
            if (cb) cb({ count: row.c, bytes: row.b });
          });
        });
      }
    };
  }

  // 9. Keep internal links inside iOS full-screen Home Screen standalone mode
  if (typeof navigator !== 'undefined' && navigator.standalone) {
    document.addEventListener('click', function (e) {
      if (e.defaultPrevented || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
      var el = e.target;
      while (el && el.tagName !== 'A') el = el.parentNode;
      if (!el || !el.getAttribute('href')) return;
      if (el.target && el.target !== '_self') return;
      if (el.hasAttribute('download')) return;
      var href = el.getAttribute('href');
      if (href.indexOf('mailto:') === 0 || href.indexOf('tel:') === 0 || href.indexOf('javascript:') === 0) return;
      if (href.charAt(0) === '#') return;
      if (el.origin && el.origin !== location.origin) return;
      e.preventDefault();
      location.href = el.href;
    }, false);
  }

  // 10. AppCache auto-swap when a newer build is ready
  if (typeof window.applicationCache !== 'undefined') {
    try {
      window.applicationCache.addEventListener('updateready', function () {
        if (window.applicationCache.status === window.applicationCache.UPDATEREADY) {
          try { window.applicationCache.swapCache(); } catch (e) {}
        }
      }, false);
    } catch (e) {}
  }
})();
