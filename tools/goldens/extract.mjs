// tools/goldens/extract.mjs
//
// Slices the live `prCalc`/`calcLiq` source straight out of the read-only
// `hr_admin_panel.html` (never written by this tool) and asserts the
// extracted bytes against a pinned SHA-256 digest.
//
// Why brace-matching instead of a fixed line range: a line-range slice goes
// silently stale the moment unrelated code is added/removed earlier in the
// file (the functions shift down/up but the slice boundaries do not move
// with them). Matching on the function *signature* and then walking brace
// depth to the matching `}` tracks the function wherever it lives. The
// SHA-256 assertion is what actually guards against drift in the function
// BODY (hazard this tool exists to catch) — see Phase 3 design R2.
//
// If `hr_admin_panel.html` ever loses `prCalc`/`calcLiq` (slice 3h deletes
// them), this throws "marker not found" and the only remaining ground truth
// becomes the verbatim vendored copy in `tools/goldens/legacy/` (see 1.7).

import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// Repo-root-relative, read-only. This tool NEVER writes to this file.
export const HTML_PATH = path.resolve(__dirname, '../../hr_admin_panel.html');

// Pinned at generation time (2026-10-08) against the slice extracted below.
// A mismatch means either (a) hr_admin_panel.html's prCalc/calcLiq changed,
// or (b) this extraction logic itself drifted — both must fail LOUDLY,
// never silently re-baseline the fixtures.
export const PINNED_SHA256 =
  '1cb8d95744f4be04ea60c3349e8e257342999c7e73394db921aaa7cf69af0a87';

function findMatchingBrace(src, openBraceIndex) {
  let depth = 0;
  for (let i = openBraceIndex; i < src.length; i++) {
    if (src[i] === '{') depth++;
    else if (src[i] === '}') {
      depth--;
      if (depth === 0) return i;
    }
  }
  throw new Error(`extract.mjs: unbalanced braces from index ${openBraceIndex}`);
}

function extractStatement(src, marker) {
  const start = src.indexOf(marker);
  if (start === -1) {
    throw new Error(
      `extract.mjs: marker not found — "${marker.slice(0, 40)}...". ` +
        `prCalc/calcLiq may have been removed or renamed in hr_admin_panel.html. ` +
        `If this is slice 3h's deletion, use tools/goldens/legacy/ instead.`,
    );
  }
  const end = src.indexOf(';', start);
  if (end === -1) {
    throw new Error(`extract.mjs: no terminating ";" found for marker "${marker}"`);
  }
  return src.slice(start, end + 1);
}

function extractFunction(src, signature) {
  const sigStart = src.indexOf(signature);
  if (sigStart === -1) {
    throw new Error(
      `extract.mjs: function signature not found — "${signature}". ` +
        `prCalc/calcLiq may have been removed or renamed in hr_admin_panel.html. ` +
        `If this is slice 3h's deletion, use tools/goldens/legacy/ instead.`,
    );
  }
  const openBrace = src.indexOf('{', sigStart);
  if (openBrace === -1) {
    throw new Error(`extract.mjs: no "{" found after signature "${signature}"`);
  }
  const closeBrace = findMatchingBrace(src, openBrace);
  return src.slice(sigStart, closeBrace + 1);
}

/**
 * Extracts the PR constant, prCalc and calcLiq from hr_admin_panel.html and
 * returns both the individual slices and the combined slice used for the
 * SHA-256 assertion.
 */
export function extractSlice({ htmlPath = HTML_PATH } = {}) {
  const src = readFileSync(htmlPath, 'utf8');

  const prConstant = extractStatement(src, 'const PR = {');
  const prCalcSrc = extractFunction(src, 'function prCalc(e, factor, attData, dedData) {');
  const calcLiqSrc = extractFunction(src, 'async function calcLiq() {');

  const combinedSlice = [prConstant, prCalcSrc, calcLiqSrc].join('\n\n');
  const sha256 = createHash('sha256').update(combinedSlice, 'utf8').digest('hex');

  return { prConstant, prCalcSrc, calcLiqSrc, combinedSlice, sha256 };
}

/**
 * Extracts and asserts the pinned digest. Throws loudly on any mismatch.
 */
export function extractAndVerify(opts) {
  const result = extractSlice(opts);
  if (result.sha256 !== PINNED_SHA256) {
    throw new Error(
      'extract.mjs: SHA-256 MISMATCH.\n' +
        `  expected: ${PINNED_SHA256}\n` +
        `  actual:   ${result.sha256}\n` +
        'hr_admin_panel.html\'s prCalc/calcLiq changed since fixtures were generated. ' +
        'This is a FAIL-LOUD guard, not a bug: regenerate fixtures deliberately ' +
        '(node tools/goldens/generate.mjs) and re-pin PINNED_SHA256 only after ' +
        'confirming the change is intentional and the new fixtures were reviewed.',
    );
  }
  return result;
}

// CLI entry: `node extract.mjs` prints the computed digest (used once, to
// pin PINNED_SHA256 above); `node extract.mjs --verify` asserts it.
if (import.meta.url === `file://${process.argv[1]}`) {
  const verifyOnly = process.argv.includes('--verify');
  const result = extractSlice();
  if (verifyOnly) {
    extractAndVerify();
    console.log('extract.mjs: OK — digest matches PINNED_SHA256');
  } else {
    console.log(result.sha256);
  }
}
