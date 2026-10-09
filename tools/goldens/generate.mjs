#!/usr/bin/env node
// tools/goldens/generate.mjs
//
// extract -> vm evaluate -> capture -> write testdata.
//
// Usage:
//   node tools/goldens/generate.mjs              regenerate committed fixtures
//   node tools/goldens/generate.mjs --check       regenerate into a temp dir and
//                                                  diff against the committed
//                                                  fixtures (non-zero exit on
//                                                  any difference)
//   node tools/goldens/generate.mjs --warn-ties   additionally flag any money
//                                                  value landing within a
//                                                  tight tolerance of a .xx5
//                                                  rounding tie, for manual
//                                                  review (generation still
//                                                  succeeds; ties are printed,
//                                                  never silently accepted
//                                                  without being surfaced)

import vm from 'node:vm';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { extractAndVerify } from './extract.mjs';
import { createCalcLiqContext } from './stubs.mjs';
import { prcalcCases } from './cases/prcalc.mjs';
import { calcliqCases } from './cases/calcliq.mjs';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const TESTDATA_DIR = path.resolve(
  __dirname,
  '../../backend/internal/payroll/testdata',
);

function buildPrCalcRunner(slice) {
  const context = { Math };
  vm.createContext(context);
  const script = `${slice.prConstant}\n\n${slice.prCalcSrc}\nglobalThis.__prCalc = prCalc;\n`;
  vm.runInContext(script, context, { filename: 'prcalc-extracted.js' });
  return (e, factor, attData, dedData) => context.__prCalc(e, factor, attData, dedData);
}

async function runCalcLiqCase(slice, fixture) {
  const { context, getCaptured, getPrintButtonOnclick } = createCalcLiqContext(fixture);
  vm.createContext(context);
  const script = `${slice.calcLiqSrc}\nglobalThis.__calcLiq = calcLiq;\n`;
  vm.runInContext(script, context, { filename: 'calcliq-extracted.js' });
  await context.__calcLiq();
  const onclick = getPrintButtonOnclick();
  if (typeof onclick !== 'function') {
    throw new Error(
      `generate.mjs: case "${fixture.name}" never reached the print-button ` +
        'wiring — calcLiq likely returned early (missing employee/empId).',
    );
  }
  onclick();
  const captured = getCaptured();
  if (!captured) {
    throw new Error(`generate.mjs: printLiq was never invoked for case "${fixture.name}"`);
  }
  return captured.breakdown;
}

function round(x, decimals) {
  const f = 10 ** decimals;
  return Math.round((x + Number.EPSILON) * f) / f;
}

/** ~.xx5 tie detector at 2-decimal rounding, for --warn-ties. */
function isNearTie(value) {
  const scaled = Math.abs(value) * 100;
  const frac = scaled - Math.floor(scaled);
  return Math.abs(frac - 0.5) < 1e-7;
}

function generatePrcalcFixtures(slice, { warnTies }) {
  const runPrCalc = buildPrCalcRunner(slice);
  const fixtures = [];
  for (const c of prcalcCases) {
    const expected = runPrCalc(c.e, c.factor, c.attData, c.dedData);
    if (warnTies) {
      for (const [field, value] of Object.entries(expected)) {
        if (typeof value === 'number' && isNearTie(value)) {
          console.warn(`[warn-ties] prcalc/${c.name}.${field} = ${value} is near a .xx5 rounding tie`);
        }
      }
    }
    fixtures.push({
      name: c.name,
      file: `${c.name}.json`,
      content: {
        name: c.name,
        note: c.note,
        js_source_sha: slice.sha256,
        input: { e: c.e, factor: c.factor, attData: c.attData, dedData: c.dedData },
        expected,
        expected_deviation: {},
      },
    });
  }
  return fixtures;
}

async function generateCalcliqFixtures(slice, { warnTies }) {
  const fixtures = [];
  for (const c of calcliqCases) {
    const breakdown = await runCalcLiqCase(slice, c);
    if (warnTies) {
      for (const [field, value] of Object.entries(breakdown)) {
        if (typeof value === 'number' && isNearTie(value)) {
          console.warn(`[warn-ties] calcliq/${c.name}.${field} = ${value} is near a .xx5 rounding tie`);
        }
      }
    }
    // years/totalMonths are genuinely unexposed: neither printLiq's argument
    // object nor the rendered innerHTML carries them at a precision usable
    // for an exact-string golden comparison (innerHTML shows `years` only
    // via `.toFixed(2)`, and `totalMonths` is never rendered at all — see
    // tools/goldens/cases/calcliq.mjs's header comment and the apply-progress
    // notes). Recording a truncated/guessed value would be worse than
    // honestly marking them unexposed.
    const expected = { ...breakdown, years: null, totalMonths: null };
    const unexposed = ['years', 'totalMonths'];

    fixtures.push({
      name: c.name,
      file: `${c.name}.json`,
      content: {
        name: c.name,
        note: c.note,
        js_source_sha: slice.sha256,
        input: {
          employee: c.employee,
          elements: c.elements,
          dedChecks: c.dedChecks || [],
        },
        expected,
        expected_deviation: c.expectedDeviation || {},
        unexposed,
        ...(c.designClaimMismatch ? { design_claim_mismatch: c.designClaimMismatch } : {}),
      },
    });
  }
  return fixtures;
}

function writeFixtures(baseDir, prcalcFixtures, calcliqFixtures) {
  const prcalcDir = path.join(baseDir, 'prcalc');
  const calcliqDir = path.join(baseDir, 'calcliq');
  fs.mkdirSync(prcalcDir, { recursive: true });
  fs.mkdirSync(calcliqDir, { recursive: true });
  for (const f of prcalcFixtures) {
    fs.writeFileSync(path.join(prcalcDir, f.file), JSON.stringify(f.content, null, 2) + '\n');
  }
  for (const f of calcliqFixtures) {
    fs.writeFileSync(path.join(calcliqDir, f.file), JSON.stringify(f.content, null, 2) + '\n');
  }
}

function diffDirs(generatedDir, committedDir) {
  const diffs = [];
  for (const sub of ['prcalc', 'calcliq']) {
    const genSub = path.join(generatedDir, sub);
    const commSub = path.join(committedDir, sub);
    const genFiles = fs.existsSync(genSub) ? fs.readdirSync(genSub).sort() : [];
    const commFiles = fs.existsSync(commSub) ? fs.readdirSync(commSub).sort() : [];
    const allFiles = Array.from(new Set([...genFiles, ...commFiles])).sort();
    for (const file of allFiles) {
      const genPath = path.join(genSub, file);
      const commPath = path.join(commSub, file);
      const genExists = fs.existsSync(genPath);
      const commExists = fs.existsSync(commPath);
      if (!genExists) {
        diffs.push(`${sub}/${file}: present in committed fixtures but NOT regenerated`);
        continue;
      }
      if (!commExists) {
        diffs.push(`${sub}/${file}: regenerated but NOT present in committed fixtures`);
        continue;
      }
      const genContent = fs.readFileSync(genPath, 'utf8');
      const commContent = fs.readFileSync(commPath, 'utf8');
      if (genContent !== commContent) {
        diffs.push(`${sub}/${file}: committed fixture does NOT byte-match regeneration`);
      }
    }
  }
  return diffs;
}

async function main() {
  const args = process.argv.slice(2);
  const checkMode = args.includes('--check');
  const warnTies = args.includes('--warn-ties');

  const slice = extractAndVerify();

  const prcalcFixtures = generatePrcalcFixtures(slice, { warnTies });
  const calcliqFixtures = await generateCalcliqFixtures(slice, { warnTies });

  if (checkMode) {
    const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'goldens-check-'));
    try {
      writeFixtures(tmpDir, prcalcFixtures, calcliqFixtures);
      const diffs = diffDirs(tmpDir, TESTDATA_DIR);
      if (diffs.length > 0) {
        console.error('generate.mjs --check: FAILED — regeneration does not match committed fixtures:');
        for (const d of diffs) console.error(`  - ${d}`);
        process.exitCode = 1;
        return;
      }
      console.log(
        `generate.mjs --check: OK — ${prcalcFixtures.length} prcalc + ` +
          `${calcliqFixtures.length} calcliq fixtures byte-match regeneration`,
      );
    } finally {
      fs.rmSync(tmpDir, { recursive: true, force: true });
    }
    return;
  }

  writeFixtures(TESTDATA_DIR, prcalcFixtures, calcliqFixtures);
  console.log(
    `generate.mjs: wrote ${prcalcFixtures.length} prcalc + ${calcliqFixtures.length} ` +
      `calcliq fixtures to ${path.relative(process.cwd(), TESTDATA_DIR)}`,
  );
}

main().catch((err) => {
  console.error(err.stack || err.message || err);
  process.exitCode = 1;
});
