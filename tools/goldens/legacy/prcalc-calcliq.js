// ════════════════════════════════════════════════════════════════════════
// VENDORED COPY — tools/goldens/legacy/prcalc-calcliq.js
//
// Verbatim extraction of `PR`, `prCalc` and `calcLiq` as they existed in
// hr_admin_panel.html at the commit/lines below. This file is NEVER edited
// by hand and NEVER regenerated automatically — it exists because slice 3h
// of the rrhh-go-table-migration phase 3 plan deletes `prCalc`/`calcLiq`
// from hr_admin_panel.html entirely. Once that happens, tools/goldens/
// extract.mjs can no longer find the functions in the live file, and THIS
// file becomes the only remaining ground truth for the golden fixtures
// under backend/internal/payroll/testdata/.
//
// Provenance:
//   Source file     : hr_admin_panel.html (repo root)
//   Source lines    : PR constant        line 1817
//                      prCalc function    lines 1870-1890
//                      async calcLiq()    lines 3580-3771
//   Source commit   : b6ce1bf3336fcdb45f7232988759d7d80dc7763e
//   Vendored on     : 2026-10-08 (SDD change rrhh-go-table-migration,
//                      Phase 3, slice 3a)
//   Slice SHA-256   : 1cb8d95744f4be04ea60c3349e8e257342999c7e73394db921aaa7cf69af0a87
//                      (identical to tools/goldens/extract.mjs's
//                      PINNED_SHA256 at the time of vendoring — both are
//                      computed over the exact same three statements,
//                      joined by two newlines, in extraction order:
//                      PR constant, prCalc, calcLiq)
//
// Regeneration after hr_admin_panel.html loses these functions (slice 3h):
// point tools/goldens/extract.mjs at this file instead of the live HTML
// and re-verify the SHA-256 above still matches before trusting any
// fixture regenerated from it.
// ════════════════════════════════════════════════════════════════════════

const PR = { CSS_E:.0975, SE_E:.0125, CSS_P:.1225, SE_P:.015, DEC:1/12 };

function prCalc(e, factor, attData, dedData) {
  factor = factor || 1;
  var salBase = (e.salary || 0) * factor;
  var attDed = 0, otAmt = 0;
  if (attData && attData.hasData) {
    attDed = ((e.salary || 0) / 30) * (attData.absentDays || 0);
    otAmt = attData.otAmount || 0;
  }
  var b = Math.max(0, salBase - attDed + otAmt);
  var css = b * PR.CSS_E, se = b * PR.SE_E;
  var annual = (b / factor) * 13; // DGI: 12 meses + Décimo 3er Mes
  var isrAnnual = annual <= 11000 ? 0 : annual <= 50000 ? (annual - 11000) * 0.15 : 5850 + (annual - 50000) * 0.25;
  var isr = (isrAnnual / 12) * factor;
  var ded = css + se + isr;
  var dedQuota = (dedData && dedData.quotaTotal) ? dedData.quotaTotal : 0;
  var net = b - ded - dedQuota;
  var pcss = b * PR.CSS_P, pse = b * PR.SE_P, dec = b * PR.DEC;
  var decCSSP = dec * 0.0725; // CSS patronal sobre Décimo (tasa especial Ley 51/2005)
  var tot = b + pcss + pse + dec + decCSSP;
  return { b:b, css:css, se:se, isr:isr, ded:ded, dedQuota:dedQuota, net:net, pcss:pcss, pse:pse, dec:dec, decCSSP:decCSSP, tot:tot, attDed:attDed, otAmt:otAmt };
}

async function calcLiq() {
  var empId=gv('liq-emp'); if(!empId) return;
  var emps=await Supa.sel('employees',{id:empId}), e=Array.isArray(emps)?emps[0]:null; if(!e) return;
  var exitDate=gv('liq-date')||TS, reason=gv('liq-reason')||'voluntaria';
  var sal=e.salary||0;
  var ms=new Date(exitDate)-new Date(e.start_date||exitDate), years=Math.max(0,ms/(365.25*86400000));
  // PayDay counts complete months then rounds: (exitYear*12+exitMonth) - (startYear*12+startMonth)
  var ed=new Date(exitDate), sd=new Date(e.start_date||exitDate);
  var totalMonths=Math.max(0,(ed.getFullYear()*12+ed.getMonth())-(sd.getFullYear()*12+sd.getMonth()));
  var floorYears=Math.round(totalMonths/12); // PayDay rounds to nearest year

  // ── Input fields ──────────────────────────────────────────────
  var salPend=parseFloat(gv('liq-sal-pend'))||0;
  var otros=parseFloat(gv('liq-otros'))||0;
  var vacDays=parseFloat(gv('liq-vac'))||0;
  var decMonths=parseFloat(gv('liq-dec'))||0;
  var acumVac=parseFloat(document.getElementById('liq-acum-vac')?document.getElementById('liq-acum-vac').value:0)||0;
  var acumDec=parseFloat(document.getElementById('liq-acum-dec')?document.getElementById('liq-acum-dec').value:0)||0;
  var acumPrima=parseFloat(document.getElementById('liq-acum-prima')?document.getElementById('liq-acum-prima').value:0)||0;
  var sal30Raw=parseFloat(document.getElementById('liq-sal30')?document.getElementById('liq-sal30').value:0)||0;
  var acum6m=parseFloat(document.getElementById('liq-acum-6m')?document.getElementById('liq-acum-6m').value:0)||0;

  // ── 1. Salario pendiente ──────────────────────────────────────
  var salario=salPend;

  // ── 2. Preaviso ───────────────────────────────────────────────
  var _prevMeses=years<0.5?sal/4.333:years<2?(sal/4.333)*2:years<5?sal:sal*2;
  var preaviso=(reason==='despido_preaviso'||reason==='pension')?_prevMeses:0;

  // ── 3. Vacaciones proporcionales ──────────────────────────────
  // (acumVac + salPend) / 11  — if acumVac provided, else fallback to days × daily rate
  var vacProp;
  if(acumVac>0){
    vacProp=(acumVac+salPend+otros)/11; // PayDay: "salarios en esta transacción" includes otros
  } else {
    var dailyRate=sal/30;
    vacProp=vacDays*dailyRate;
  }

  // ── 4. Décimo tercer mes proporcional ─────────────────────────
  // (acumDec + vacProp + salPend + otros) / 12  — fallback: (sal/12)*months
  var decProp;
  if(acumDec>0){
    decProp=(acumDec+vacProp+salPend+otros)/12; // PayDay includes vac+sal+otros de la transacción
  } else {
    decProp=(sal/12)*decMonths;
  }

  // ── 5. Gasto de representación (always 0) ─────────────────────
  // ── 6. Otros ingresos/gastos ──────────────────────────────────
  // (already in `otros`)

  // ── Parcial 1-4+6 ────────────────────────────────────────────
  var parcial=salario+preaviso+vacProp+decProp+otros;

  // ── 7. Prima de antigüedad ────────────────────────────────────
  var primaMonths=acumPrima>0?Math.min(Math.round(years*12),60):Math.min(floorYears*12,60);
  if(primaMonths<1) primaMonths=1;
  var primaTotal=acumPrima>0?acumPrima:(sal*12*Math.min(floorYears,5));
  var primaMensual=primaTotal/primaMonths;
  var primaSemanal=primaMensual/4.3333;
  var antigSem=primaSemanal*floorYears; // 7.4 auto
  var primaDeduccion=reason==='sin_prev'?primaSemanal:0; // 7.6
  var antigSemNeta=antigSem-primaDeduccion; // 7.7

  // ── 8. Indemnización ─────────────────────────────────────────
  var indem6mMensual=acum6m>0?(acum6m/6):sal;
  var sal30=sal30Raw>0?sal30Raw:sal;
  var indemSemanalFav=Math.max(indem6mMensual,sal30)/4.3333; // 8.4 más favorable
  var indemWeeks=floorYears<=10?floorYears:10+(floorYears-10)*2; // 8.5 weeks
  var indemBase=indemSemanalFav*indemWeeks; // 8.5
  var recargoPct=reason==='indem_25'?0.25:reason==='indem_50'?0.50:0;
  var recargo=indemBase*recargoPct; // 8.6
  var indemnizacion=0; // 8.8
  if(reason==='injustificada'||reason==='acuerdo'){ indemnizacion=indemBase; }
  else if(reason==='indem_25'){ indemnizacion=indemBase+recargo; }
  else if(reason==='indem_50'){ indemnizacion=indemBase+recargo; }

  // ── Total antes de deducciones ────────────────────────────────
  var total=parcial+antigSemNeta+indemnizacion;

  // ── 9. Descuentos legales ─────────────────────────────────────
  // 9.1 + 9.2: CSS base = salPend + vacProp + decProp + preaviso + otros
  var cssBase=salPend+vacProp+decProp+preaviso+otros;
  var css91=cssBase*0.0975;
  var se92=cssBase*0.0125;
  // 9.3 ISR pts 1-6
  var annual=sal*13;
  var isrAnual=annual<=11000?0:annual<=50000?(annual-11000)*0.15:5850+(annual-50000)*0.25;
  var isrRate=annual>0?isrAnual/annual:0;
  var isr93=cssBase*isrRate;
  // 9.4 ISR pts 7 y 8 (Art. 701)
  var art701Base=antigSemNeta+indemnizacion;
  var art701Sujeta=Math.max(0,art701Base-art701Base*(floorYears/100)-5000);
  var isr94=art701Sujeta<=11000?0:art701Sujeta<=50000?(art701Sujeta-11000)*0.15:5850+(art701Sujeta-50000)*0.25;
  var totalLegal=css91+se92+isr93+isr94;

  // ── Personal deductions ───────────────────────────────────────
  var checkedDeds=[]; document.querySelectorAll('.liq-ded-check:checked').forEach(function(cb){ checkedDeds.push({desc:cb.dataset.desc,quota:parseFloat(cb.dataset.quota)||0}); });
  var totalDed=checkedDeds.reduce(function(s,d){ return s+d.quota; },0);
  var netTotal=total-totalLegal-totalDed;

  // ── Show result card ──────────────────────────────────────────
  var card=document.getElementById('liq-result-card'), ph=document.getElementById('liq-placeholder');
  if(card) card.style.display=''; if(ph) ph.style.display='none';
  var rl={voluntaria:'Renuncia Voluntaria',sin_prev:'Renuncia sin Preaviso (Art. 222)',acuerdo:'Mutuo Acuerdo',justificada:'Despido Justificado',injustificada:'Despido Injustificado',despido_preaviso:'Despido con Preaviso (Art. 212)',indem_25:'Indemnización +25%',indem_50:'Indemnización +50%',pension:'Jubilación',vencimiento:'Vencimiento de Contrato'};
  var reasonBadgeClass=reason==='injustificada'||reason==='indem_25'||reason==='indem_50'?'badge-danger':reason==='voluntaria'||reason==='pension'||reason==='vencimiento'?'badge-success':'badge-warning';

  function fmtL(n){ return 'B/. '+n.toFixed(2); }

  var body=document.getElementById('liq-result-body');
  if(!body) return;

  body.innerHTML=
    // ── Header ──
    '<div class="row mb-2 align-items-center">'
      +'<div class="col-7"><strong class="d-block">'+e.first_name+' '+e.last_name+'</strong>'
        +'<small class="text-muted">'+(e.position||'—')+' · '+(e.department||'—')+'</small></div>'
      +'<div class="col-5 text-right"><span class="badge '+reasonBadgeClass+' px-2 py-1 d-block mb-1">'+(rl[reason]||reason)+'</span>'
        +'<small class="text-muted">Egreso: '+exitDate+'</small></div></div>'
    +'<div class="alert alert-light py-1 mb-2 small"><i class="fas fa-info-circle mr-1 text-info"></i>'
      +'Tiempo trabajado: <strong>'+years.toFixed(2)+' años</strong>'
      +'&nbsp;|&nbsp;Semanas prima: <strong>'+floorYears+'</strong>'
      +'&nbsp;|&nbsp;Semanas indem.: <strong>'+indemWeeks+'</strong></div>'
    // ── Breakdown table ──
    +'<div class="table-responsive"><table class="table table-sm mb-1" style="font-size:.82rem">'
      +'<thead class="thead-light"><tr><th style="width:2rem">Línea</th><th>Concepto</th><th class="text-right">Calculado</th></tr></thead>'
      +'<tbody>'
      +'<tr><td class="font-weight-bold text-muted">1.</td><td>Salario</td><td class="text-right text-success font-weight-bold">'+fmtL(salario)+'</td></tr>'
      +'<tr><td class="font-weight-bold text-muted">2.</td><td>Preaviso</td><td class="text-right text-success font-weight-bold">'+fmtL(preaviso)+'</td></tr>'
      +'<tr><td class="font-weight-bold text-muted">3.</td><td>Vacaciones Proporcionales</td><td class="text-right text-success font-weight-bold">'+fmtL(vacProp)+'</td></tr>'
      +(acumVac>0?'<tr><td></td><td class="text-muted small pl-3">('+fmtL(acumVac)+' acum. + '+fmtL(salPend)+' sal.) / 11</td><td></td></tr>':'')
      +'<tr><td class="font-weight-bold text-muted">4.</td><td>Décimo Tercer Mes Proporcional</td><td class="text-right text-success font-weight-bold">'+fmtL(decProp)+'</td></tr>'
      +(acumDec>0?'<tr><td></td><td class="text-muted small pl-3">('+fmtL(acumDec)+' acum. + '+fmtL(vacProp)+' vac. + '+fmtL(salPend)+' sal.) / 12</td><td></td></tr>':'')
      +'<tr><td class="font-weight-bold text-muted">5.</td><td class="text-muted">Gasto de Representación</td><td class="text-right text-muted">'+fmtL(0)+'</td></tr>'
      +'<tr><td class="font-weight-bold text-muted">6.</td><td>Otros Ingresos y Gastos</td><td class="text-right text-success font-weight-bold">'+fmtL(otros)+'</td></tr>'
      +'<tr class="table-active"><td colspan="2" class="font-weight-bold text-right">PARCIAL (1–4 + 6)</td><td class="text-right font-weight-bold">'+fmtL(parcial)+'</td></tr>'
      // 7. Prima
      +'<tr class="table-info"><td class="font-weight-bold">7.</td><td colspan="2" class="font-weight-bold">PRIMA DE ANTIGÜEDAD</td></tr>'
      +'<tr><td class="text-muted small">7.1</td><td class="pl-3 small">Base para el cálculo: '+fmtL(primaTotal)+' ('+primaMonths+' meses)</td><td></td></tr>'
      +'<tr><td class="text-muted small">7.2</td><td class="pl-3 small">Promedio mensual: '+fmtL(primaTotal)+' / '+primaMonths+' = '+fmtL(primaMensual)+'</td><td></td></tr>'
      +'<tr><td class="text-muted small">7.3</td><td class="pl-3 small">Promedio semanal: '+fmtL(primaMensual)+' / 4.3333 = '+fmtL(primaSemanal)+'</td><td></td></tr>'
      +'<tr><td class="text-muted small">7.4</td><td class="pl-3 small">Prima (auto): '+fmtL(primaSemanal)+' × '+floorYears+' sem.</td><td class="text-right small">'+fmtL(antigSem)+'</td></tr>'
      +'<tr><td class="text-muted small">7.5</td><td class="pl-3 small">Prima (manual)</td><td class="text-right small">'+fmtL(antigSem)+'</td></tr>'
      +(primaDeduccion>0?'<tr><td class="text-muted small">7.6</td><td class="pl-3 small text-danger">Menos preaviso Art. 222</td><td class="text-right small text-danger">- '+fmtL(primaDeduccion)+'</td></tr>':'')
      +'<tr><td class="text-muted small font-weight-bold">7.7</td><td class="pl-3 font-weight-bold">PRIMA A PAGAR</td><td class="text-right font-weight-bold text-success">'+fmtL(antigSemNeta)+'</td></tr>'
      // 8. Indemnización
      +'<tr class="table-warning"><td class="font-weight-bold">8.</td><td colspan="2" class="font-weight-bold">INDEMNIZACIÓN</td></tr>'
      +'<tr><td class="text-muted small">8.1</td><td class="pl-3 small">Salario últimos 6 meses: '+(acum6m>0?fmtL(acum6m):'(6 × sal. actual)')+'</td><td></td></tr>'
      +'<tr><td class="text-muted small">8.2</td><td class="pl-3 small">Promedio mensual: '+fmtL(indem6mMensual)+'</td><td></td></tr>'
      +'<tr><td class="text-muted small">8.3</td><td class="pl-3 small">Salario últimos 30 días: '+fmtL(sal30)+'</td><td></td></tr>'
      +'<tr><td class="text-muted small">8.4</td><td class="pl-3 small">Promedio semanal (más favorable): max('+fmtL(indem6mMensual)+', '+fmtL(sal30)+') / 4.3333</td><td class="text-right small">'+fmtL(indemSemanalFav)+'</td></tr>'
      +'<tr><td class="text-muted small">8.5</td><td class="pl-3 small">Indemnización: '+fmtL(indemSemanalFav)+' × '+indemWeeks+' sem.</td><td class="text-right small">'+fmtL(indemBase)+'</td></tr>'
      +(recargo>0?'<tr><td class="text-muted small">8.6</td><td class="pl-3 small">Recargo '+(recargoPct*100)+'%</td><td class="text-right small text-warning">'+fmtL(recargo)+'</td></tr>':'')
      +'<tr><td class="text-muted small">8.7</td><td class="pl-3 small">Indemnización (manual)</td><td class="text-right small">'+fmtL(indemnizacion)+'</td></tr>'
      +'<tr><td class="text-muted small font-weight-bold">8.8</td><td class="pl-3 font-weight-bold">INDEMNIZACIÓN A PAGAR</td><td class="text-right font-weight-bold text-success">'+fmtL(indemnizacion)+'</td></tr>'
      +'<tr class="table-success"><td colspan="2" class="font-weight-bold text-right">TOTAL ANTES DE DEDUCCIONES</td><td class="text-right font-weight-bold">'+fmtL(total)+'</td></tr>'
      // 9. Descuentos
      +'<tr class="table-danger"><td class="font-weight-bold">9.</td><td colspan="2" class="font-weight-bold">DESCUENTOS LEGALES</td></tr>'
      +'<tr><td class="text-muted small">9.1</td><td class="pl-3 small">Seguro Social (9.75%) — base: '+fmtL(cssBase)+'</td><td class="text-right small text-danger">- '+fmtL(css91)+'</td></tr>'
      +'<tr><td class="text-muted small">9.2</td><td class="pl-3 small">Seguro Educativo (1.25%)</td><td class="text-right small text-danger">- '+fmtL(se92)+'</td></tr>'
      +'<tr><td class="text-muted small">9.3</td><td class="pl-3 small">ISR pts 1 al 6 (tasa efectiva '+( isrRate*100).toFixed(2)+'%)</td><td class="text-right small text-danger">- '+fmtL(isr93)+'</td></tr>'
      +'<tr><td class="text-muted small">9.4</td><td class="pl-3 small">ISR pts 7 y 8 — Art. 701 (base sujeta: '+fmtL(art701Sujeta)+')</td><td class="text-right small text-danger">- '+fmtL(isr94)+'</td></tr>'
      +'<tr class="table-danger"><td colspan="2" class="text-right font-weight-bold text-danger">Total deducciones legales</td><td class="text-right font-weight-bold text-danger">- '+fmtL(totalLegal)+'</td></tr>'
      +'</tbody></table></div>'
    // Personal deductions
    +(checkedDeds.length?'<div class="alert alert-danger py-2 small mb-2"><strong>Deducciones personales:</strong>'
      +checkedDeds.map(function(d){ return '<div class="d-flex justify-content-between"><span>(-) '+d.desc+'</span><span>- '+fmtL(d.quota)+'</span></div>'; }).join('')
      +'</div>':'')
    // Net card
    +'<div class="card bg-success text-white mt-2 mb-2"><div class="card-body p-3"><div class="row align-items-center">'
      +'<div class="col-6"><h5 class="mb-0"><i class="fas fa-dollar-sign mr-2"></i>NETO A PAGAR</h5>'
        +'<small style="opacity:.8">Bruto: '+fmtL(total)+' — Ret.: '+fmtL(totalLegal)+(totalDed>0?' — Ded.: '+fmtL(totalDed):'')+'</small></div>'
      +'<div class="col-6 text-right"><h4 class="mb-0"><strong>'+fmtL(netTotal)+'</strong></h4></div>'
    +'</div></div></div>'
    // Save + legal
    +'<div class="text-right mt-2"><button class="btn btn-info btn-sm mr-2" onclick="saveLiqHistory(\''+empId+'\',\''+e.first_name+' '+e.last_name+'\',\''+reason+'\',\''+exitDate+'\','+netTotal+')"><i class="fas fa-save mr-1"></i>Guardar</button></div>'
    +'<div class="mt-2 alert alert-warning small"><i class="fas fa-gavel mr-1"></i>Cálculo referencial según Arts. 225-230 C. Trabajo · Ley 51/2005 CSS · Código Fiscal Panamá.</div>';

  document.getElementById('liq-print-btn').onclick=function(){
    printLiq(e.first_name+' '+e.last_name,rl[reason]||reason,exitDate,{
      salario:salario,preaviso:preaviso,vacProp:vacProp,decProp:decProp,otros:otros,parcial:parcial,
      primaTotal:primaTotal,primaMonths:primaMonths,primaMensual:primaMensual,primaSemanal:primaSemanal,
      antigSem:antigSem,primaDeduccion:primaDeduccion,antigSemNeta:antigSemNeta,
      indem6mMensual:indem6mMensual,sal30:sal30,indemSemanalFav:indemSemanalFav,
      indemBase:indemBase,recargo:recargo,recargoPct:recargoPct,indemnizacion:indemnizacion,
      total:total,css91:css91,se92:se92,isr93:isr93,isr94:isr94,cssBase:cssBase,
      art701Sujeta:art701Sujeta,isrRate:isrRate,floorYears:floorYears,indemWeeks:indemWeeks,
      totalLegal:totalLegal,checkedDeds:checkedDeds,totalDed:totalDed,netTotal:netTotal
    });
  };
}