// tools/errors/harness.mjs
//
// Drives the REAL apiErrMsg of hr_admin_panel.html (plus API_FIELD_ES and, once
// it exists, API_CONFLICT_ES) in a node:vm sandbox. Sources are sliced out of
// the HTML and executed as written.
//
// apiErrMsg turns the Go API error envelope {code, message, fields} into the
// Spanish toast text. The server messages for not_found, bad_request, internal
// and the generic conflicts are English ("not found", "invalid body"...), so
// they must never reach the user as-is; recruitment conflicts are already
// Spanish and keep their detail.
//
// Run: node tools/errors/harness.mjs   (exit code 1 on any failed assertion)

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import { fileURLToPath } from 'node:url';

const html = readFileSync(process.env.ERRORS_HTML ||
  path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../hr_admin_panel.html'), 'utf8');

// Source of `header ... { ... }` by brace matching that skips strings/comments.
// Only used on code without regex literals.
function extractFunction(src, header) {
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
const objectLiteral = (header) => (html.includes(header) ? extractFunction(html, header) + ';' : '');

const srcs = [
  objectLiteral('var API_FIELD_ES='),
  objectLiteral('var API_CONFLICT_ES='),
  extractFunction(html, 'function apiErrMsg('),
].join('\n');

const sandbox = { Object, String };
vm.createContext(sandbox);
vm.runInContext(srcs, sandbox);
const msg = (err, fallback = 'No se pudo guardar', o) => {
  sandbox.__args = [err, fallback, o];
  return vm.runInContext('apiErrMsg.apply(null, __args)', sandbox);
};
const e = (code, message, extra) => Object.assign({ code, message }, extra);

const tests = [];
const test = (name, fn) => tests.push([name, fn]);

test('not_found without a screen text: Spanish reason, not "not found"', () => {
  assert.equal(msg(e('not_found', 'not found')), 'No se pudo guardar: el registro no existe');
});
test('bad_request: Spanish reason, not "invalid body"', () => {
  assert.equal(msg(e('bad_request', 'invalid body')), 'No se pudo guardar: la solicitud no es válida');
});
test('internal: Spanish reason, not "internal error"', () => {
  assert.equal(msg(e('internal', 'internal error')), 'No se pudo guardar: error interno del servidor');
});
test('internal from a non-JSON HTTP failure ("HTTP 502"): Spanish reason', () => {
  assert.equal(msg(e('internal', 'HTTP 502')), 'No se pudo guardar: error interno del servidor');
});
test('unauthorized: Spanish reason', () => {
  assert.equal(msg(e('unauthorized', 'invalid token')), 'No se pudo guardar: la sesión expiró');
});
test('network (browser "Failed to fetch"): Spanish reason', () => {
  assert.equal(msg(e('network', 'Failed to fetch')), 'No se pudo guardar: no se pudo conectar con el servidor');
});
test('known English conflicts are translated', () => {
  assert.equal(msg(e('conflict', 'already exists')), 'Ya existe un registro con esos datos');
  assert.equal(msg(e('conflict', 'leave request already decided')), 'La solicitud ya fue resuelta');
  assert.equal(msg(e('conflict', 'referenced record missing')), 'Un registro relacionado ya no existe');
  assert.equal(msg(e('conflict', 'company must keep at least one active admin')), 'La empresa debe conservar al menos un administrador activo');
});

test('Spanish conflicts from the server keep their detail (unchanged)', () => {
  assert.equal(msg(e('conflict', '2 postulación(es) siguen asociadas a esta vacante')), '2 postulación(es) siguen asociadas a esta vacante');
});
test('screen overrides win (unchanged)', () => {
  assert.equal(msg(e('not_found', 'not found'), 'x', { notFound: 'La vacante ya no existe' }), 'La vacante ya no existe');
  assert.equal(msg(e('conflict', 'whatever'), 'x', { conflict: () => 'Mensaje propio' }), 'Mensaje propio');
  assert.equal(msg(e('forbidden', 'forbidden'), 'x', { forbidden: 'Solo administradores' }), 'Solo administradores');
});
test('forbidden, validation_failed and rate_limited keep their Spanish texts (unchanged)', () => {
  assert.equal(msg(e('forbidden', 'forbidden')), 'No tienes permiso para esta acción');
  assert.equal(msg(e('validation_failed', 'validation failed', { fields: { quota: 'required' } })), 'Datos inválidos: revisa ' + (vm.runInContext('API_FIELD_ES.quota', sandbox) || 'quota'));
  assert.equal(msg(e('rate_limited', 'too many requests')), 'Demasiadas solicitudes, intenta de nuevo en un momento');
});
test('unknown codes keep the server reason, and no error object gives the fallback (unchanged)', () => {
  assert.equal(msg(e('teapot', 'brewing')), 'No se pudo guardar: brewing');
  assert.equal(msg(null), 'No se pudo guardar');
});

let failed = 0;
for (const [name, fn] of tests) {
  try { await fn(); console.log('ok   ' + name); }
  catch (err) { failed++; console.log('FAIL ' + name + '\n     ' + String(err.message).split('\n').join('\n     ')); }
}
console.log(`${tests.length - failed}/${tests.length} passed`);
process.exit(failed ? 1 : 0);
