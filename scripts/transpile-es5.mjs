#!/usr/bin/env node
// Post-Hugo ES5 transpilation pass for iOS 9.3.5 / Safari 9 compatibility.
//
// - Transpiles public/js/*.js to pure ES5 via @swc/core.
// - Transpiles unique inline <script> blocks across public HTML files,
//   deduplicated by SHA-256 so ~2,000 Hugo pages finish quickly.
// - Verifies every output script parses cleanly as strict ECMAScript 5 via acorn.
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
  // Prevent accidental </script> inside inline HTML scripts
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

// 1. Verify legacy-polyfill.js itself is valid ES5
const polyfillPath = path.join(PUBLIC_DIR, 'legacy-polyfill.js');
if (fs.existsSync(polyfillPath)) {
  const polyCode = fs.readFileSync(polyfillPath, 'utf8');
  acorn.parse(polyCode, { ecmaVersion: 5, sourceType: 'script' });
}

// 2. Transpile external JS under public/js/
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

// 3. Transpile inline <script> blocks across public HTML files (deduplicated by hash)
const htmlFiles = walk(PUBLIC_DIR, '.html');
const cache = new Map();
let uniqueTranspiled = 0;
let htmlModified = 0;

const SCRIPT_RE = /<script([^>]*)>([\s\S]*?)<\/script>/gi;

for (const file of htmlFiles) {
  const src = fs.readFileSync(file, 'utf8');
  let changed = false;
  const out = src.replace(SCRIPT_RE, (match, attrs, body) => {
    if (!body || !body.trim()) return match;
    const typeMatch = attrs.match(/\btype\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))/i);
    if (typeMatch) {
      const t = (typeMatch[1] || typeMatch[2] || typeMatch[3] || '').toLowerCase();
      if (t && t !== 'text/javascript' && t !== 'application/javascript') return match;
    }
    const hash = crypto.createHash('sha256').update(body).digest('hex');
    let transformed;
    if (cache.has(hash)) {
      transformed = cache.get(hash);
    } else {
      transformed = transpileES5(body, `${file}#inline(${hash.slice(0, 8)})`);
      if (transformed !== body) uniqueTranspiled++;
      cache.set(hash, transformed);
    }
    if (transformed !== body) {
      changed = true;
      return `<script${attrs}>${transformed}</script>`;
    }
    return match;
  });
  if (changed) {
    fs.writeFileSync(file, out, 'utf8');
    htmlModified++;
  }
}

const secs = ((Date.now() - t0) / 1000).toFixed(2);
console.log(
  `ES5 transpile complete in ${secs}s: ` +
  `${jsTranspiled}/${jsFiles.length} JS files, ` +
  `${uniqueTranspiled} unique inline scripts across ${htmlModified}/${htmlFiles.length} HTML files (all ES5-verified).`
);
