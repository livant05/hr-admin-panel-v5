/**
 * =============================================================
 * LEAVE & VACATIONS VIEW — HR ADMIN PANEL (PANAMÁ)
 * =============================================================
 * Módulo de vacaciones. Computa saldos acumulados de acuerdo con
 * el Art. 54 del Código de Trabajo, procesa solicitudes y provee
 * flujos de aprobación.
 */

import { State } from '../state.js';
import { DB } from '../supabase.js';

export default {
  async render(container) {
    this.showMiniLoader(container);

    // Cargar empleados, balances e historial de solicitudes
    await Promise.all([
      DB.select('employees'),
      DB.select('leaveBalances'),
      DB.select('leaveRequests')
    ]);

    const employees = State.get('employees') || [];
    const balances = State.get('leaveBalances') || [];
    const requests = State.get('leaveRequests') || [];
    const session = State.get('session');

    container.innerHTML = `
      <div class="page-header animate-fade-in">
        <div>
          <h1 class="page-title"><i class="fa-solid fa-umbrella-beach"></i> Control de Vacaciones</h1>
          <p class="page-subtitle">Gestioná balances, procesá solicitudes y aprobaciones de descanso (Art. 54 C. Trabajo).</p>
        </div>
      </div>

      <div style="display: grid; grid-template-columns: 1.1fr 2fr; gap: 32px; align-items: start;" class="animate-fade-in">
        
        <!-- LEFT COLUMN: SOLICITUD / SALDOS -->
        <div style="display: flex; flex-direction: column; gap: 32px;">
          
          <!-- SOLICITAR VACACIONES FORM -->
          <div class="glass-card" style="border-top: 3px solid var(--accent-warning);">
            <h3 style="font-family: var(--font-title); font-weight: 700; margin-bottom: 20px; font-size: 1.15rem;"><i class="fa-solid fa-paper-plane"></i> Crear Solicitud</h3>
            
            <form id="form-leave-request" style="display: flex; flex-direction: column; gap: 16px;">
              <div class="form-group">
                <label for="req-emp-select">Colaborador</label>
                <select id="req-emp-select" class="form-control" required>
                  <option value="">Seleccione...</option>
                  ${employees.filter(e => e.status === 'active').map(e => `
                    <option value="${e.id}">${e.first_name} ${e.last_name}</option>
                  `).join('')}
                </select>
              </div>

              <!-- Balance Preview Indicator -->
              <div id="balance-preview" style="background: rgba(255,255,255,0.02); padding: 12px; border-radius: 8px; border: 1px solid var(--border-glass); display: none; justify-content: space-between; font-size: 0.78rem;">
                <span>Días Acumulados: <strong style="color: var(--accent-success);" id="prev-accrued">0</strong></span>
                <span>Tomados: <strong style="color: var(--accent-danger);" id="prev-used">0</strong></span>
                <span>Disponibles: <strong style="color: var(--accent-primary);" id="prev-remaining">0</strong></span>
              </div>

              <div class="form-grid" style="grid-template-columns: 1fr 1fr; margin-bottom: 0;">
                <div class="form-group">
                  <label for="req-start">Desde *</label>
                  <input type="date" id="req-start" class="form-control" required>
                </div>
                <div class="form-group">
                  <label for="req-end">Hasta *</label>
                  <input type="date" id="req-end" class="form-control" required>
                </div>
              </div>

              <div class="form-group" style="background: rgba(255,255,255,0.01); border: 1px dashed var(--border-glass); border-radius: 10px; padding: 12px; text-align: center; font-size: 0.88rem;">
                Total días calendario solicitados: <strong id="lbl-days" style="color: var(--accent-warning);">0</strong>
              </div>

              <div class="form-group">
                <label for="req-notes">Comentarios / Justificación</label>
                <textarea id="req-notes" class="form-control" rows="2" placeholder="Opcional..."></textarea>
              </div>

              <button type="submit" class="glass-btn primary" style="width: 100%; margin-top: 8px;"><i class="fa-solid fa-circle-check"></i> Enviar Solicitud</button>
            </form>
          </div>

        </div>

        <!-- RIGHT COLUMN: LOG DE SOLICITUDES -->
        <div class="glass-card" style="padding: 0; overflow: hidden;">
          <div style="padding: 24px;">
            <h3 style="font-family: var(--font-title); font-weight: 700; margin-bottom: 16px; font-size: 1.15rem;">Solicitudes Pendientes y Aprobadas</h3>
          </div>

          <div class="table-responsive">
            <table class="custom-table">
              <thead>
                <tr>
                  <th>Colaborador</th>
                  <th>Período</th>
                  <th style="text-align: center;">Días</th>
                  <th>Estado</th>
                  <th style="text-align: center;">Acciones</th>
                </tr>
              </thead>
              <tbody id="requests-tbody">
                <!-- Se inyectarán dinámicamente -->
              </tbody>
            </table>
          </div>
        </div>

      </div>
    `;

    this.renderRows(requests, session);
    this.bindEvents(container, employees, balances);
  },

  renderRows(requests, session) {
    const tbody = document.getElementById('requests-tbody');
    if (!tbody) return;

    if (requests.length === 0) {
      tbody.innerHTML = `
        <tr>
          <td colspan="5" style="text-align: center; color: var(--color-text-muted); padding: 40px;">
            <i class="fa-solid fa-umbrella-beach" style="font-size: 2rem; margin-bottom: 12px;"></i><br>
            No hay solicitudes de vacaciones registradas.
          </td>
        </tr>
      `;
      return;
    }

    // El usuario de RRHH/Admin puede aprobar/rechazar
    const canApprove = session && (session.role === 'Admin' || session.role === 'RRHH');

    tbody.innerHTML = requests.map(req => `
      <tr class="animate-fade-in" style="animation-duration: 0.2s;">
        <td>
          <div style="font-weight: 600;">${req.employee_name}</div>
          <div style="font-size: 0.72rem; color: var(--color-text-muted);">Solicitud: ${req.type}</div>
        </td>
        <td>
          <div style="font-size: 0.85rem;"><strong>${req.start_date}</strong> a <strong>${req.end_date}</strong></div>
          <div style="font-size: 0.72rem; color: var(--color-text-muted); max-width: 200px; text-overflow: ellipsis; white-space: nowrap; overflow: hidden;" title="${req.notes || ''}">${req.notes || 'Sin notas'}</div>
        </td>
        <td style="text-align: center; font-weight: 700; color: var(--accent-warning);">${req.days}</td>
        <td>
          <span class="badge-custom ${req.status === 'approved' ? 'success' : req.status === 'pending' ? 'warning' : 'danger'}">
            ${req.status === 'approved' ? 'Aprobada' : req.status === 'pending' ? 'Pendiente' : 'Rechazada'}
          </span>
        </td>
        <td>
          <div style="display: flex; gap: 8px; justify-content: center;">
            ${req.status === 'pending' && canApprove
              ? `
                <button class="glass-btn btn-approve-req" data-id="${req.id}" title="Aprobar Solicitud" style="padding: 6px 10px; font-size: 0.8rem; border-color: var(--accent-success); color: var(--accent-success); background: rgba(16,185,129,0.05);">
                  <i class="fa-solid fa-circle-check"></i>
                </button>
                <button class="glass-btn danger btn-reject-req" data-id="${req.id}" title="Rechazar Solicitud" style="padding: 6px 10px; font-size: 0.8rem;">
                  <i class="fa-solid fa-circle-xmark"></i>
                </button>
                `
              : `<span style="font-size: 0.72rem; color: var(--color-text-muted);">Procesado</span>`
            }
          </div>
        </td>
      </tr>
    `).join('');
  },

  bindEvents(container, employees, balances) {
    const selectEmp = container.querySelector('#req-emp-select');
    const prevWrap = container.querySelector('#balance-preview');
    const startInput = container.querySelector('#req-start');
    const endInput = container.querySelector('#req-end');
    const lblDays = container.querySelector('#lbl-days');
    const formReq = container.querySelector('#form-leave-request');

    // EVENTO: Mostrar balance al seleccionar empleado
    if (selectEmp) {
      selectEmp.addEventListener('change', () => {
        const id = selectEmp.value;
        if (!id) {
          prevWrap.style.display = 'none';
          return;
        }

        const bal = balances.find(b => b.employee_id === id);
        if (bal) {
          prevWrap.style.display = 'flex';
          container.querySelector('#prev-accrued').textContent = Number(bal.earned_days).toFixed(1);
          container.querySelector('#prev-used').textContent = Number(bal.used_days).toFixed(1);
          container.querySelector('#prev-remaining').textContent = (bal.earned_days - bal.used_days).toFixed(1);
        } else {
          // Si no tiene balance inicial, asumimos 0
          prevWrap.style.display = 'flex';
          container.querySelector('#prev-accrued').textContent = '0.0';
          container.querySelector('#prev-used').textContent = '0.0';
          container.querySelector('#prev-remaining').textContent = '0.0';
        }
      });
    }

    // EVENTO: Calcular días calendario automáticamente
    const calcDays = () => {
      const start = startInput.value;
      const end = endInput.value;
      if (start && end) {
        const diff = new Date(end) - new Date(start);
        const days = Math.ceil(diff / (1000 * 60 * 60 * 24)) + 1; // Inclusive
        if (days > 0) {
          lblDays.textContent = days;
        } else {
          lblDays.textContent = '0';
        }
      }
    };
    if (startInput) startInput.addEventListener('change', calcDays);
    if (endInput) endInput.addEventListener('change', calcDays);

    // EVENTO: Crear Solicitud
    if (formReq) {
      formReq.addEventListener('submit', async (e) => {
        e.preventDefault();
        const empId = selectEmp.value;
        const start = startInput.value;
        const end = endInput.value;
        const days = Number(lblDays.textContent);
        const notes = container.querySelector('#req-notes').value.trim();

        if (days <= 0) {
          alert('La fecha de salida debe ser posterior a la de inicio.');
          return;
        }

        const emp = employees.find(e => e.id === empId);

        // Validar saldo disponible de vacaciones
        const bal = balances.find(b => b.employee_id === empId);
        const remaining = bal ? bal.earned_days - bal.used_days : 0;

        if (days > remaining) {
          if (!confirm(`Advertencia: El colaborador solo tiene ${remaining.toFixed(1)} días disponibles y está solicitando ${days} días. ¿Deseas proceder de todas formas?`)) {
            return;
          }
        }

        const record = {
          employee_id: empId,
          employee_name: `${emp.first_name} ${emp.last_name}`,
          type: 'vacaciones',
          start_date: start,
          end_date: end,
          days: days,
          status: 'pending',
          notes: notes
        };

        const { error } = await DB.insert('leaveRequests', record);
        if (!error) {
          alert('Solicitud de vacaciones enviada con éxito.');
          this.render(document.getElementById('app-content'));
        } else {
          alert('Error al enviar solicitud: ' + error.message);
        }
      });
    }

    // ACCIONES: Aprobar / Rechazar
    const tbody = container.querySelector('#requests-tbody');
    if (tbody) {
      tbody.addEventListener('click', async (e) => {
        const appBtn = e.target.closest('.btn-approve-req');
        const rejBtn = e.target.closest('.btn-reject-req');

        if (appBtn) {
          const reqId = appBtn.getAttribute('data-id');
          const requests = State.get('leaveRequests') || [];
          const req = requests.find(r => r.id === reqId);

          if (confirm(`¿Aprobar la solicitud de vacaciones de ${req.days} días para ${req.employee_name}?`)) {
            // 1. Aprobar la solicitud
            const { error } = await DB.update('leaveRequests', reqId, { status: 'approved' });
            
            if (!error) {
              // 2. Descontar del balance de vacaciones del colaborador
              const bal = balances.find(b => b.employee_id === req.employee_id);
              if (bal) {
                const newUsed = Number(bal.used_days) + Number(req.days);
                await DB.update('leaveBalances', bal.id, {
                  used_days: newUsed
                });
              }
              
              alert('Solicitud aprobada y balance deducido.');
              this.render(document.getElementById('app-content'));
            } else {
              alert('Error al procesar: ' + error.message);
            }
          }
        }

        if (rejBtn) {
          const reqId = rejBtn.getAttribute('data-id');
          if (confirm('¿Rechazar esta solicitud de vacaciones?')) {
            const { error } = await DB.update('leaveRequests', reqId, { status: 'rejected' });
            if (!error) {
              alert('Solicitud rechazada.');
              this.render(document.getElementById('app-content'));
            } else {
              alert('Error: ' + error.message);
            }
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
