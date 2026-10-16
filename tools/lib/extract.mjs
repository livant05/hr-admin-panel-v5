// tools/lib/extract.mjs
//
// Shared by the tools/*/harness.mjs files: slice a function's real source out
// of hr_admin_panel.html so a harness can run it as written in a node:vm
// sandbox. Nothing is reimplemented.

import assert from 'node:assert/strict';

// Source of `header ... { ... }` by brace matching that skips strings and
// comments. Regex literals are NOT understood: a regex containing a quote or a
// brace breaks the matching, so page code that harnesses extract avoids them.
export function extractFunction(src, header) {
  const at = src.indexOf(header);
  assert.ok(at >= 0, `${header} not found in hr_admin_panel.html`);
  let i = src.indexOf('{', at);
  let depth = 0;
  for (; i < src.length; i++) {
    const c = src[i];
    if (c === '"' || c === "'" || c === '`') {
      for (i++; src[i] !== c; i++) if (src[i] === '\\') i++;
    } else if (c === '/' && src[i + 1] === '/') {
      i = src.indexOf('\n', i);
    } else if (c === '/' && src[i + 1] === '*') {
      i = src.indexOf('*/', i) + 1;
    } else if (c === '{') depth++;
    else if (c === '}' && --depth === 0) return src.slice(at, i + 1);
  }
  throw new Error(`unbalanced braces after ${header}`);
}

// Like extractFunction for a header written without `async`, keeping the
// keyword when the page declares the function async.
export function extractMaybeAsync(src, header) {
  const fn = extractFunction(src, header);
  const at = src.indexOf(header);
  return src.slice(Math.max(0, at - 6), at) === 'async ' ? 'async ' + fn : fn;
}
