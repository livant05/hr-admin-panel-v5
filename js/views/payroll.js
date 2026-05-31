/**
 * =============================================================
 * PAYROLL VIEW — HR ADMIN PANEL (PANAMÁ)
 * =============================================================
 * Módulo de Planilla Quincenal. Ejecuta auditorías sobre los
 * salarios brutos de colaboradores, retiene Seguro Social y Educativo,
 * integra descuentos recurrentes y archiva históricos de pagos.
 */

import { State } from '../state.js';
import { DB } from '../supabase.js';
import { PanamaLaw } from '../utils/panamaLaw.js';

export default {
  async render(container) {
    this.showMiniLoader(container);

    // Cargar empleados, deducciones, horas extras y logs históricos
    await Promise.all([
      DB.select('employees'),
      DB.select('deductions'),
      DB.select('overtime'),
      DB.select('payrollHistory')
    ]);

    const employees = State.get('employees') || [];
    const activeEmployees = employees.filter(e => e.status === 'active');
    const deductions = State.get('deductions') || [];
    const overtime = State.get('overtime') || [];
    const history = State.get('payrollHistory') || [];

    // Calcular período actual
    const today = new Date();
    const isFirstQuincena = today.getDate() <= 15;
    const periodName = `${isFirstQuincena ? '1ra' : '2da'} Quincena de ${today.toLocaleString('es-PA', { month: 'long' })} ${today.getFullYear()}`;

    // Computar planilla global en memoria
    const computedRows = activeEmployees.map(emp => {
      // Salario Quincenal = Salario Mensual / 2
      const basePeriodSalary = Number(emp.salary) / 2;

      // Obtener horas extras acumuladas del período
      const empOtLogs = overtime.filter(ot => ot.employee_id === emp.id);
      const otEarnings = empOtLogs.reduce((sum, log) => sum + Number(log.amount), 0);

      // Obtener deducciones de préstamos activas
      const empDeds = deductions.filter(d => d.employee_id === emp.id && d.status === 'active');
      const loansDeductions = empDeds.reduce((sum, log) => sum + Number(log.quota), 0);

      // Realizar cálculos matemáticos utilizando el motor legal panameño
      const calculations = PanamaLaw.calculatePayroll(basePeriodSalary, otEarnings, 0, loansDeductions);

      return {
        employee: emp,
        basePeriodSalary,
        otEarnings,
        loansDeductions,
        ...calculations
      };
    });

    // Totales globales de la planilla quincenal
    const totalGross = computedRows.reduce((sum, r) => sum + r.grossSalary, 0);
    const totalNet = computedRows.reduce((sum, r) => sum + r.netSalary, 0);
    const totalEmployerCost = computedRows.reduce((sum, r) => sum + r.totalEmployerCost, 0);

    container.innerHTML = `
      <div class="page-header animate-fade-in">
        <div>
          <h1 class="page-title"><i class="fa-solid fa-money-check-dollar"></i> Planilla Quincenal</h1>
          <p class="page-subtitle">Cálculo contable quincenal con retenciones de Seguro Social (CSS) y Seguro Educativo (SE).</p>
        </div>
        <div>
          <button class="glass-btn primary" id="btn-archive-payroll"><i class="fa-solid fa-box-archive"></i> Archivar Planilla</button>
        </div>
      </div>

      <!-- SUMMARY TOP CARDS -->
      <div class="stats-grid animate-fade-in" style="margin-bottom: 24px;">
        <div class="glass-card stat-card" style="border-left: 3px solid var(--accent-secondary);">
          <div class="stat-info">
            <span class="stat-label">Período Activo</span>
            <span class="stat-value" style="font-size: 1.15rem; margin-top: 8px; font-weight: 700;">${periodName}</span>
          </div>
          <div class="stat-icon" style="color: var(--accent-secondary);"><i class="fa-solid fa-calendar-check"></i></div>
        </div>

        <div class="glass-card stat-card" style="border-left: 3px solid var(--accent-primary);">
          <div class="stat-info">
            <span class="stat-label">Bruto Global</span>
            <span class="stat-value">$${totalGross.toFixed(2)}</span>
          </div>
          <div class="stat-icon"><i class="fa-solid fa-sack-dollar"></i></div>
        </div>

        <div class="glass-card stat-card" style="border-left: 3px solid var(--accent-success);">
          <div class="stat-info">
            <span class="stat-label">Líquido Neto</span>
            <span class="stat-value">$${totalNet.toFixed(2)}</span>
          </div>
          <div class="stat-icon" style="color: var(--accent-success);"><i class="fa-solid fa-vault"></i></div>
        </div>

        <div class="glass-card stat-card" style="border-left: 3px solid var(--accent-warning);">
          <div class="stat-info">
            <span class="stat-label">Costo Corporativo</span>
            <span class="stat-value">$${totalEmployerCost.toFixed(2)}</span>
          </div>
          <div class="stat-icon" style="color: var(--accent-warning);"><i class="fa-solid fa-chart-line"></i></div>
        </div>
      </div>

      <!-- PLANILLA DETALLADA TABLE -->
      <div class="glass-card animate-fade-in" style="padding: 0; overflow: hidden; margin-bottom: 32px;">
        <div class="table-responsive">
          <table class="custom-table" style="font-size: 0.82rem;">
            <thead>
              <tr>
                <th>Colaborador</th>
                <th>S. Quincenal</th>
                <th>H. Extras</th>
                <th>S. Social (9.75%)</th>
                <th>S. Educ. (1.25%)</th>
                <th>Préstamos</th>
                <th>Neto Pagar</th>
                <th style="text-align: center;">Talonario</th>
              </tr>
            </thead>
            <tbody>
              ${computedRows.map(row => `
                <tr>
                  <td>
                    <div style="font-weight: 600;">${row.employee.first_name} ${row.employee.last_name}</div>
                    <span style="font-size: 0.68rem; color: var(--color-text-muted);">${row.employee.position}</span>
                  </td>
                  <td style="font-weight: 500;">$${row.basePeriodSalary.toFixed(2)}</td>
                  <td style="color: var(--accent-secondary); font-weight: 500;">+$${row.otEarnings.toFixed(2)}</td>
                  <td style="color: var(--accent-danger); font-weight: 500;">-$${row.cssEmployee.toFixed(2)}</td>
                  <td style="color: var(--accent-danger); font-weight: 500;">-$${row.seEmployee.toFixed(2)}</td>
                  <td style="color: var(--accent-warning); font-weight: 500;">-$${row.loansDeductions.toFixed(2)}</td>
                  <td style="font-weight: 700; color: var(--accent-success);">$${row.netSalary.toFixed(2)}</td>
                  <td style="text-align: center;">
                    <button class="glass-btn btn-print-slip" 
                      data-first="${row.employee.first_name}"
                      data-last="${row.employee.last_name}"
                      data-pos="${row.employee.position}"
                      data-dept="${row.employee.department}"
                      data-ced="${row.employee.cedula}"
                      data-ss="${row.employee.ss_number}"
                      data-gross="${row.grossSalary.toFixed(2)}"
                      data-css="${row.cssEmployee.toFixed(2)}"
                      data-se="${row.seEmployee.toFixed(2)}"
                      data-ded="${row.loansDeductions.toFixed(2)}"
                      data-net="${row.netSalary.toFixed(2)}"
                      data-period="${periodName}"
                      style="padding: 6px 12px; font-size: 0.78rem;">
                      <i class="fa-solid fa-receipt"></i> Recibo
                    </button>
                  </td>
                </tr>
              `).join('')}
            </tbody>
          </table>
        </div>
      </div>

      <!-- MODAL TALONARIO (Oculto inicialmente) -->
      <div id="payroll-modal-overlay" style="display: none; position: fixed; top: 0; left: 0; right: 0; bottom: 0; background: rgba(0,0,0,0.6); backdrop-filter: blur(8px); z-index: 1000; align-items: center; justify-content: center; padding: 20px;">
        <!-- Talonario imprimible inyectado aquí -->
      </div>
    `;

    this.bindEvents(container, computedRows, totalGross, totalNet, totalEmployerCost, periodName);
  },

  bindEvents(container, computedRows, totalGross, totalNet, totalEmployerCost, periodName) {
    const modalOverlay = document.getElementById('payroll-modal-overlay');

    // EVENTO: Mostrar Talonario individual imprimible
    const printButtons = container.querySelectorAll('.btn-print-slip');
    printButtons.forEach(btn => {
      btn.addEventListener('click', (e) => {
        const d = btn.dataset;
        
        modalOverlay.style.display = 'flex';
        modalOverlay.innerHTML = `
          <div class="glass-card animate-fade-in" style="max-width: 600px; width: 100%; background: #ffffff; color: #1a1a2e; padding: 32px; box-shadow: 0 10px 40px rgba(0,0,0,0.25);">
            <!-- PRINT CONTAINER -->
            <div id="printable-receipt" style="border: 2px solid #1a1a2e; padding: 20px; font-family: 'Inter', sans-serif;">
              <div style="display: flex; justify-content: space-between; border-bottom: 2px solid #1a1a2e; padding-bottom: 12px; margin-bottom: 20px;">
                <div>
                  <h3 style="font-family: 'Outfit', sans-serif; font-weight: 800; font-size: 1.15rem; text-transform: uppercase;">Corporación Transístmica S.A.</h3>
                  <span style="font-size: 0.72rem; color: #555;">RUC: 1552364-1-654321 DV 89</span>
                </div>
                <div style="text-align: right;">
                  <h4 style="font-weight: 700; font-size: 0.9rem;">TALONARIO DE PLANILLA</h4>
                  <span style="font-size: 0.72rem; color: #555;">Período: ${d.period}</span>
                </div>
              </div>

              <!-- DATOS COLABORADOR -->
              <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 10px; font-size: 0.78rem; margin-bottom: 20px; background: #f8f9fa; padding: 10px; border-radius: 6px;">
                <div>Colaborador: <strong>${d.first} ${d.last}</strong></div>
                <div>Cédula: <strong>${d.ced}</strong></div>
                <div>Cargo: <strong>${d.pos}</strong></div>
                <div>Seguro Social: <strong>${d.ss}</strong></div>
              </div>

              <!-- DESGLOSE ECONÓMICO -->
              <table style="width: 100%; font-size: 0.78rem; border-collapse: collapse; margin-bottom: 20px;">
                <thead>
                  <tr style="border-bottom: 1px solid #1a1a2e;">
                    <th style="text-align: left; padding: 6px 0;">Concepto</th>
                    <th style="text-align: right; padding: 6px 0;">Ingresos (+)</th>
                    <th style="text-align: right; padding: 6px 0;">Retenciones (-)</th>
                  </tr>
                </thead>
                <tbody>
                  <tr>
                    <td style="padding: 6px 0;">Salario Base Quincenal</td>
                    <td style="text-align: right;">$${(Number(d.gross) - Number(d.gross) * 0.0).toFixed(2)}</td>
                    <td style="text-align: right;">-</td>
                  </tr>
                  <tr>
                    <td style="padding: 6px 0;">Seguro Social (CSS 9.75%)</td>
                    <td style="text-align: right;">-</td>
                    <td style="text-align: right;">$${d.css}</td>
                  </tr>
                  <tr>
                    <td style="padding: 6px 0;">Seguro Educativo (SE 1.25%)</td>
                    <td style="text-align: right;">-</td>
                    <td style="text-align: right;">$${d.se}</td>
                  </tr>
                  ${Number(d.ded) > 0 
                    ? `<tr>
                        <td style="padding: 6px 0;">Descuentos / Préstamos</td>
                        <td style="text-align: right;">-</td>
                        <td style="text-align: right;">$${d.ded}</td>
                       </tr>`
                    : ''
                  }
                </tbody>
                <tfoot>
                  <tr style="border-top: 2px solid #1a1a2e; font-weight: 700; font-size: 0.85rem;">
                    <td style="padding: 10px 0;">LÍQUIDO NETO A RECIBIR</td>
                    <td colspan="2" style="text-align: right; color: #10b981; font-size: 0.95rem;">$${d.net}</td>
                  </tr>
                </tfoot>
              </table>

              <!-- FIRMAS -->
              <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 40px; margin-top: 40px; font-size: 0.72rem; text-align: center;">
                <div style="border-top: 1px solid #1a1a2e; padding-top: 8px;">Entregado por la Empresa</div>
                <div style="border-top: 1px solid #1a1a2e; padding-top: 8px;">Firma del Colaborador</div>
              </div>
            </div>

            <!-- CONTROLES -->
            <div style="display: flex; gap: 12px; margin-top: 24px;">
              <button class="glass-btn primary" id="btn-print-action" style="flex: 1;"><i class="fa-solid fa-print"></i> Imprimir Recibo</button>
              <button class="glass-btn danger" id="btn-close-slip-modal" style="flex: 0.4;">Cerrar</button>
            </div>
          </div>
        `;

        // Cerrar modal
        overlay.querySelector('#btn-close-slip-modal').addEventListener('click', () => {
          overlay.style.display = 'none';
        });

        // Lanzar impresión física del DOM del recibo
        overlay.querySelector('#btn-print-action').addEventListener('click', () => {
          const printContent = document.getElementById('printable-receipt').outerHTML;
          const originalContent = document.body.innerHTML;
          
          document.body.innerHTML = printContent;
          window.print();
          window.location.reload(); // Recarga para restaurar el estado completo de la SPA
        });
      });
    });

    // EVENTO: Archivar Planilla Quincenal en Histórico
    const btnArchive = container.querySelector('#btn-archive-payroll');
    if (btnArchive) {
      btnArchive.addEventListener('click', async () => {
        if (confirm(`¿Seguro que deseas dar por procesada, pagada y archivar la planilla del período "${periodName}"? Se registrará en el historial financiero.`)) {
          const record = {
            period: periodName,
            month: new Date().getMonth() + 1,
            year: new Date().getFullYear(),
            month_name: new Date().toLocaleString('es-PA', { month: 'long' }),
            employee_count: computedRows.length,
            total_bruto: totalGross,
            total_neto: totalNet,
            total_empresa: totalEmployerCost
          };

          const { error } = await DB.insert('payrollHistory', record);
          if (!error) {
            alert(`Planilla del período "${periodName}" archivada correctamente.`);
            this.render(container);
          } else {
            alert('Error al archivar planilla: ' + error.message);
          }
        }
      });
    }
  },

  showMiniLoader(container) {
    container.innerHTML = `
      <div style="display: flex; align-items: center; justify-content: center; min-height: 60vh;">
        <i class="fa-solid fa-circle-notch fa-spin" style="font-size: 2.5rem; color: var(--accent-primary);"></i>
      </div>
    `;
  }
};
export const renderExtraView = async (path, wrapper) => {
  // Manejo de módulos adicionales consolidado en extraModules.js
};
