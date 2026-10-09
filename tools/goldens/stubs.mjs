// tools/goldens/stubs.mjs
//
// node:vm globals that let the extracted `prCalc`/`calcLiq` source run
// unmodified. `prCalc` is a pure function and needs none of this — it is
// called directly. `calcLiq` reads form fields via `gv`/`document`, fetches
// the employee via `Supa.sel`, and hands its complete breakdown to
// `printLiq` (hr_admin_panel.html:3759-3770) before the function returns.
// Stubbing `printLiq` captures that exact object with no DOM, no browser,
// and no HTML scraping (Phase 3 design R2).

/**
 * @param {object} fixture — one calcliq case (see cases/calcliq.mjs)
 * @returns {{ context: object, getCaptured: () => object|null }}
 */
export function createCalcLiqContext(fixture) {
  const elementValues = { ...fixture.elements };
  const elementStore = new Map();
  let captured = null;

  function makeElement(id) {
    if (elementStore.has(id)) return elementStore.get(id);
    const el = {
      get value() {
        return elementValues[id] !== undefined ? String(elementValues[id]) : '';
      },
      set value(v) {
        elementValues[id] = v;
      },
      innerHTML: '',
      style: {},
      classList: {
        add() {},
        remove() {},
        contains() {
          return false;
        },
      },
      onclick: null,
      dataset: {},
    };
    elementStore.set(id, el);
    return el;
  }

  const document = {
    getElementById(id) {
      return makeElement(id);
    },
    querySelectorAll(selector) {
      if (selector === '.liq-ded-check:checked') {
        return (fixture.dedChecks || [])
          .filter((d) => d.checked)
          .map((d) => ({ dataset: { desc: d.desc, quota: String(d.quota) } }));
      }
      return [];
    },
  };

  // `gv(id)` — mirrors hr_admin_panel.html:2158's `.value.trim()` contract,
  // sourced from the same fixture-backed element map `document` uses, so a
  // case only has to declare each input once.
  function gv(id) {
    const v = elementValues[id];
    return v === undefined || v === null ? '' : String(v).trim();
  }

  const Supa = {
    async sel(table, filter) {
      if (table === 'employees') {
        return [fixture.employee];
      }
      throw new Error(`stubs.mjs: unexpected Supa.sel('${table}') during calcLiq capture`);
    },
    async ins() {
      throw new Error('stubs.mjs: calcLiq must not call Supa.ins — only read paths are exercised');
    },
  };

  function printLiq(name, reasonLabel, exitDate, breakdown) {
    captured = { name, reasonLabel, exitDate, breakdown };
  }

  const fmt = (n) => 'B/. ' + Number(n).toFixed(2);
  const toast = () => {};

  const context = {
    document,
    gv,
    Supa,
    printLiq,
    fmt,
    toast,
    window: { open: () => ({ document: { write() {}, close() {} } }) },
    Math,
    Date,
    Array,
    Object,
    console,
    parseFloat,
  };

  return {
    context,
    getCaptured: () => captured,
    getPrintButtonOnclick: () => {
      const btn = elementStore.get('liq-print-btn');
      return btn ? btn.onclick : null;
    },
  };
}
