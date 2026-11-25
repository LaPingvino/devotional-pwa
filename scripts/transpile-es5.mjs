#!/usr/bin/env node
// Post-Hugo legacy Safari 8/9 (iOS 8.4.1 & 9.3.5) compatibility pass:
//
// 1. JS: Transpiles public/js/*.js and unique inline <script> blocks across
//    public HTML files to pure ES5 via @swc/core, verified with acorn (ecma5).
// 2. CSS: Adds static fallback declarations before var(--...) (Safari 8 lacks
//    CSS custom properties) and adds -webkit- flexbox prefixes (Safari 8 needs
//    prefixed flex) across public/css/*.css, <style> blocks, and inline style=.
import crypto from 'node:crypto';
import fs from 'node:fs';
import { createRequire } from 'node:module';
import path from 'node:path';

const toolsDir = process.env.ES5_TOOLS_DIR ? path.resolve(process.env.ES5_TOOLS_DIR) : process.cwd();
const req = createRequire(path.join(toolsDir, 'package.json'));
const swc = req('@swc/core');
const acorn = req('acorn');

const PUBLIC_DIR = process.argv[2] || 'public';
const t0 = Date.now();

const SWC_OPTS = {
  jsc: {
    parser: { syntax: 'ecmascript' },
    target: 'es5',
    loose: false,
    externalHelpers: false,
    minify: { compress: true, mangle: true },
  },
  minify: true,
  sourceMaps: false,
  isModule: false,
};

function needsTranspile(code) {
  try {
    acorn.parse(code, { ecmaVersion: 5, sourceType: 'script' });
    return false;
  } catch {
    return true;
  }
}

function transpileES5(code, label) {
  if (!needsTranspile(code)) return code;
  const result = swc.transformSync(code, SWC_OPTS);
  const out = result.code.replace(/<\/script>/gi, '<\\/script>');
  try {
    acorn.parse(out, { ecmaVersion: 5, sourceType: 'script' });
  } catch (err) {
    throw new Error(`ES5 verification failed for ${label}: ${err.message}`);
  }
  return out;
}

function walk(dir, ext, out = []) {
  if (!fs.existsSync(dir)) return out;
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) walk(full, ext, out);
    else if (entry.name.endsWith(ext)) out.push(full);
  }
  return out;
}

// ── CSS legacy helpers (iOS 8.4.1: var() static fallbacks + -webkit-flex) ──

function extractRootVars(cssText) {
  const vars = Object.create(null);
  const rootRe = /:root\s*\{([^}]*)\}/g;
  let m;
  while ((m = rootRe.exec(cssText)) !== null) {
    const decls = m[1].split(';');
    for (const d of decls) {
      const idx = d.indexOf(':');
      if (idx < 0) continue;
      const k = d.slice(0, idx).trim();
      const v = d.slice(idx + 1).trim();
      if (k.startsWith('--') && v) vars[k] = v;
    }
  }
  return vars;
}

function resolveVarsInValue(val, vars) {
  let out = val;
  for (let pass = 0; pass < 3 && out.includes('var('); pass++) {
    out = out.replace(/var\(\s*(--[a-zA-Z0-9_-]+)\s*(?:,\s*([^)]+))?\)/g, (full, name, fb) => {
      if (Object.prototype.hasOwnProperty.call(vars, name)) return vars[name];
      if (fb !== undefined) return fb.trim();
      return full;
    });
  }
  return out.includes('var(') ? null : out;
}

const WEBKIT_FLEX_PROPS = new Set([
  'flex',
  'flex-direction',
  'flex-wrap',
  'flex-flow',
  'flex-grow',
  'flex-shrink',
  'flex-basis',
  'justify-content',
  'align-items',
  'align-self',
  'align-content',
  'order',
]);

function splitDeclarations(block) {
  const parts = [];
  let cur = '';
  let depth = 0;
  let quote = '';
  for (let i = 0; i < block.length; i++) {
    const ch = block[i];
    if (quote) {
      cur += ch;
      if (ch === '\\' && i + 1 < block.length) { cur += block[++i]; continue; }
      if (ch === quote) quote = '';
      continue;
    }
    if (ch === '"' || ch === "'") { quote = ch; cur += ch; continue; }
    if (ch === '(') depth++;
    else if (ch === ')' && depth > 0) depth--;
    if (ch === ';' && depth === 0) {
      parts.push(cur);
      cur = '';
    } else {
      cur += ch;
    }
  }
  if (cur.trim()) parts.push(cur);
  return parts;
}

function transformDeclList(declBlock, vars) {
  const decls = splitDeclarations(declBlock);
  const out = [];
  for (let i = 0; i < decls.length; i++) {
    const raw = decls[i];
    const trimmed = raw.trim();
    if (!trimmed) continue;
    const colon = trimmed.indexOf(':');
    if (colon <= 0) { out.push(raw); continue; }
    const prop = trimmed.slice(0, colon).trim();
    const val = trimmed.slice(colon + 1).trim();
    const propLC = prop.toLowerCase();
    const prevTrim = out.length > 0 ? out[out.length - 1].trim().toLowerCase() : '';

    // Add -webkit- flexbox prefix for Safari 8
    if (propLC === 'display' && (val === 'flex' || val === 'inline-flex')) {
      const wkVal = '-webkit-' + val;
      if (prevTrim !== `display:${wkVal}` && prevTrim !== `display: ${wkVal}`) {
        out.push(`display:${wkVal}`);
      }
    } else if (WEBKIT_FLEX_PROPS.has(propLC)) {
      const wkProp = '-webkit-' + propLC;
      if (!prevTrim.startsWith(wkProp + ':')) {
        const wkVal = val.includes('var(') ? (resolveVarsInValue(val, vars) || val) : val;
        if (!wkVal.includes('var(')) out.push(`${wkProp}:${wkVal}`);
      }
    }

    // Add static fallback before var(--...) declaration for Safari 8
    if (!propLC.startsWith('--') && val.includes('var(')) {
      const resolved = resolveVarsInValue(val, vars);
      if (resolved && resolved !== val) {
        const expectedPrev = `${propLC}:${resolved.toLowerCase()}`;
        if (prevTrim !== expectedPrev) {
          out.push(`${prop}:${resolved}`);
        }
      }
    }

    out.push(trimmed);
  }
  return out.join(';');
}

function transformStylesheet(cssText, vars) {
  // Process leaf declaration blocks (blocks containing no nested '{' or '}')
  // Iterates cleanly over regular rules and rules nested inside @media / @supports.
  return cssText.replace(/\{([^{}]+)\}/g, (match, inner) => {
    // Only transform if it contains var( or flex properties
    if (!inner.includes('var(') && !inner.includes('flex') && !inner.includes('align-') && !inner.includes('justify-content')) {
      return match;
    }
    const updated = transformDeclList(inner, vars);
    return `{${updated}}`;
  });
}

// ── Main pipeline ──

// 1. Verify legacy-polyfill.js itself is valid ES5
const polyfillPath = path.join(PUBLIC_DIR, 'legacy-polyfill.js');
if (fs.existsSync(polyfillPath)) {
  const polyCode = fs.readFileSync(polyfillPath, 'utf8');
  acorn.parse(polyCode, { ecmaVersion: 5, sourceType: 'script' });
}

// 2. Collect :root CSS variables and patch public/css/*.css
const cssFiles = walk(path.join(PUBLIC_DIR, 'css'), '.css');
const rootVars = Object.create(null);
for (const file of cssFiles) {
  Object.assign(rootVars, extractRootVars(fs.readFileSync(file, 'utf8')));
}
let cssPatched = 0;
for (const file of cssFiles) {
  const src = fs.readFileSync(file, 'utf8');
  const out = transformStylesheet(src, rootVars);
  if (out !== src) {
    fs.writeFileSync(file, out, 'utf8');
    cssPatched++;
  }
}

// 3. Transpile external JS under public/js/
const jsFiles = walk(path.join(PUBLIC_DIR, 'js'), '.js');
let jsTranspiled = 0;
for (const file of jsFiles) {
  const src = fs.readFileSync(file, 'utf8');
  const out = transpileES5(src, file);
  if (out !== src) {
    fs.writeFileSync(file, out, 'utf8');
    jsTranspiled++;
  }
}

// 4. Transpile inline <script>, patch inline <style>, and add var() fallbacks to style= attrs
const htmlFiles = walk(PUBLIC_DIR, '.html');
const jsCache = new Map();
const cssCache = new Map();
let uniqueJsTranspiled = 0;
let uniqueStylePatched = 0;
let htmlModified = 0;

const SCRIPT_RE = /<script([^>]*)>([\s\S]*?)<\/script>/gi;
const STYLE_TAG_RE = /<style([^>]*)>([\s\S]*?)<\/style>/gi;
const INLINE_STYLE_QUOTED_RE = /\bstyle=(["'])([^"']*var\(--[^"']*)\1/gi;
const INLINE_STYLE_UNQUOTED_RE = /\bstyle=([^\s"'<>]*var\(--[^\s"'<>]*)/gi;

for (const file of htmlFiles) {
  const src = fs.readFileSync(file, 'utf8');
  let out = src;

  out = out.replace(SCRIPT_RE, (match, attrs, body) => {
    if (!body || !body.trim()) return match;
    const typeMatch = attrs.match(/\btype\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))/i);
    if (typeMatch) {
      const t = (typeMatch[1] || typeMatch[2] || typeMatch[3] || '').toLowerCase();
      if (t && t !== 'text/javascript' && t !== 'application/javascript') return match;
    }
    const hash = crypto.createHash('sha256').update(body).digest('hex');
    let transformed;
    if (jsCache.has(hash)) {
      transformed = jsCache.get(hash);
    } else {
      transformed = transpileES5(body, `${file}#inline(${hash.slice(0, 8)})`);
      if (transformed !== body) uniqueJsTranspiled++;
      jsCache.set(hash, transformed);
    }
    return transformed !== body ? `<script${attrs}>${transformed}</script>` : match;
  });

  out = out.replace(STYLE_TAG_RE, (match, attrs, body) => {
    if (!body || !body.trim()) return match;
    const hash = crypto.createHash('sha256').update(body).digest('hex');
    let transformed;
    if (cssCache.has(hash)) {
      transformed = cssCache.get(hash);
    } else {
      transformed = transformStylesheet(body, rootVars);
      if (transformed !== body) uniqueStylePatched++;
      cssCache.set(hash, transformed);
    }
    return transformed !== body ? `<style${attrs}>${transformed}</style>` : match;
  });

  if (out.includes('style=') && out.includes('var(--')) {
    out = out.replace(INLINE_STYLE_QUOTED_RE, (match, q, val) => {
      const updated = transformDeclList(val, rootVars);
      return updated !== val ? `style=${q}${updated}${q}` : match;
    });
    out = out.replace(INLINE_STYLE_UNQUOTED_RE, (match, val) => {
      const updated = transformDeclList(val, rootVars);
      if (updated === val) return match;
      // Quote if fallback introduces spaces (e.g. "1px solid #ddd")
      return /\s/.test(updated) ? `style="${updated}"` : `style=${updated}`;
    });
  }

  if (out !== src) {
    fs.writeFileSync(file, out, 'utf8');
    htmlModified++;
  }
}

const secs = ((Date.now() - t0) / 1000).toFixed(2);
console.log(
  `Legacy ES5 + CSS pass complete in ${secs}s: ` +
  `${jsTranspiled}/${jsFiles.length} JS files, ` +
  `${cssPatched}/${cssFiles.length} CSS files (${Object.keys(rootVars).length} vars), ` +
  `${uniqueJsTranspiled} unique inline scripts & ${uniqueStylePatched} unique <style> blocks ` +
  `across ${htmlModified}/${htmlFiles.length} HTML files (all ES5-verified).`
);
