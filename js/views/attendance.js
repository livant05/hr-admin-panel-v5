/**
 * =============================================================
 * ATTENDANCE VIEW — HR ADMIN PANEL (PANAMÁ)
 * =============================================================
 * Módulo de registro diario y control de tiempos. Monitorea
 * puntualidades en base a la jornada laboral, y mantiene un
 * log histórico auditable e imprimible.
 */

import { State } from '../state.js';
import { DB } from '../supabase.js';

export default {
  async render(container) {
    this.showMiniLoader(container);

    // Cargar colaboradores y registros
    await Promise.all([
      DB.select('employees'),
      DB.select('attendance')
    ]);

    const employees = State.get('employees') || [];
    const attendance = State.get('attendance') || [];

    // Maquetar HTML de la vista
    container.innerHTML = `
      <div class="page-header animate-fade-in">
        <div>
          <h1 class="page-title"><i class="fa-solid fa-fingerprint"></i> Control de Asistencia</h1>
          <p class="page-subtitle">Monitoreá la puntualidad, registrá marcajes virtuales e historial del equipo.</p>
        </div>
      </div>

      <div style="display: grid; grid-template-columns: 1.2fr 2fr; gap: 32px; align-items: start;" class="animate-fade-in">
        
        <!-- LEFT COLUMN: RELOJ Y MARCAJE VIRTUAL -->
        <div style="display: flex; flex-direction: column; gap: 32px;">
          
          <div class="glass-card text-center" style="border-top: 3px solid var(--accent-secondary); padding: 32px 24px;">
            <h3 style="font-family: var(--font-title); font-weight: 700; margin-bottom: 12px; font-size: 1.15rem;">Marcaje Virtual</h3>
            
            <!-- Real-time Clock display -->
            <div id="live-clock" style="font-family: var(--font-title); font-size: 2.25rem; font-weight: 800; background: var(--accent-gradient); -webkit-background-clip: text; -webkit-text-fill-color: transparent; margin-bottom: 8px;">00:00:00</div>
            <div id="live-date" style="font-size: 0.8rem; color: var(--color-text-muted); margin-bottom: 24px;">Miércoles, 27 de Mayo de 2026</div>

            <form id="form-clock-in" style="display: flex; flex-direction: column; gap: 16px;">
              <div class="form-group" style="text-align: left;">
                <label for="clock-emp-select">Seleccionar Colaborador</label>
                <select id="clock-emp-select" class="form-control" required>
                  <option value="">Seleccione...</option>
                  ${employees.filter(e => e.status === 'active').map(e => `
                    <option value="${e.id}">${e.first_name} ${e.last_name}</option>
                  `).join('')}
                </select>
              </div>

              <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 12px; margin-top: 10px;">
                <button type="button" class="glass-btn primary" id="btn-clock-in" style="height: 48px; justify-content: center;"><i class="fa-solid fa-right-to-bracket"></i> Entrada</button>
                <button type="button" class="glass-btn" id="btn-clock-out" style="height: 48px; justify-content: center; background: rgba(255,255,255,0.05);"><i class="fa-solid fa-right-from-bracket"></i> Salida</button>
              </div>
            </form>
          </div>

          <!-- SUMMARY METRICS -->
          <div class="glass-card">
            <h3 style="font-family: var(--font-title); font-weight: 700; margin-bottom: 16px; font-size: 1.15rem;">Resumen de Hoy</h3>
            <div style="display: flex; flex-direction: column; gap: 12px;">
              <div style="display: flex; justify-content: space-between; font-size: 0.85rem;">
                <span style="color: var(--color-text-secondary);">Presentes / Puntuales:</span>
                <strong style="color: var(--accent-success);" id="stat-present">0</strong>
              </div>
              <div style="display: flex; justify-content: space-between; font-size: 0.85rem;">
                <span style="color: var(--color-text-secondary);">Tardanzas:</span>
                <strong style="color: var(--accent-warning);" id="stat-late">0</strong>
              </div>
              <div style="display: flex; justify-content: space-between; font-size: 0.85rem;">
                <span style="color: var(--color-text-secondary);">Ausencias:</span>
                <strong style="color: var(--accent-danger);" id="stat-absent">0</strong>
              </div>
            </div>
          </div>

        </div>

        <!-- RIGHT COLUMN: HISTORIAL DE MARCAJES -->
        <div class="glass-card" style="padding: 0; overflow: hidden;">
          <div style="padding: 24px;">
            <h3 style="font-family: var(--font-title); font-weight: 700; margin-bottom: 16px; font-size: 1.15rem;">Historial de Marcajes</h3>
            <div style="display: grid; grid-template-columns: 2fr 1.2fr; gap: 16px;">
              <input type="text" id="search-att" class="form-control" placeholder="Buscar por colaborador...">
              <select id="filter-att-status" class="form-control">
                <option value="">Todos los estados</option>
                <option value="present">Puntual</option>
                <option value="late">Tardanza</option>
                <option value="absent">Ausente</option>
              </select>
            </div>
          </div>

          <div class="table-responsive">
            <table class="custom-table">
              <thead>
                <tr>
                  <th>Colaborador</th>
                  <th>Fecha</th>
                  <th>Entrada</th>
                  <th>Salida</th>
                  <th>Estado</th>
                  <th style="text-align: center;">Notas</th>
                </tr>
              </thead>
              <tbody id="attendance-tbody">
                <!-- Se inyectarán dinámicamente -->
              </tbody>
            </table>
          </div>
        </div>

      </div>
    `;

    this.startClock();
    this.renderRows(attendance);
    this.updateStats(attendance);
    this.bindEvents(container);
  },

  startClock() {
    const clockEl = document.getElementById('live-clock');
    const dateEl = document.getElementById('live-date');
    if (!clockEl || !dateEl) return;

    const updateTime = () => {
      const now = new Date();
      clockEl.textContent = now.toLocaleTimeString('es-PA', { hour12: false });
      dateEl.textContent = now.toLocaleDateString('es-PA', { weekday: 'long', year: 'numeric', month: 'long', day: 'numeric' });
    };

    updateTime();
    this.clockInterval = setInterval(updateTime, 1000);
  },

  renderRows(logs) {
    const tbody = document.getElementById('attendance-tbody');
    if (!tbody) return;

    if (logs.length === 0) {
      tbody.innerHTML = `
        <tr>
          <td colspan="6" style="text-align: center; color: var(--color-text-muted); padding: 40px;">
            <i class="fa-solid fa-clock-rotate-left" style="font-size: 2rem; margin-bottom: 12px;"></i><br>
            No hay registros de marcaje registrados hoy.
          </td>
        </tr>
      `;
      return;
    }

    tbody.innerHTML = logs.slice(0, 15).map(log => `
      <tr class="animate-fade-in" style="animation-duration: 0.2s;">
        <td>
          <div style="font-weight: 600;">${log.employee_name || 'Desconocido'}</div>
          <div style="font-size: 0.72rem; color: var(--color-text-muted);">${log.department || 'Operaciones'}</div>
        </td>
        <td style="font-size: 0.85rem;">${log.date}</td>
        <td style="font-weight: 600; color: var(--accent-secondary); font-size: 0.85rem;">${log.time_in || '--:--'}</td>
        <td style="font-weight: 600; color: var(--color-text-muted); font-size: 0.85rem;">${log.time_out || '--:--'}</td>
        <td>
          <span class="badge-custom ${log.status === 'present' ? 'success' : log.status === 'late' ? 'warning' : 'danger'}">
            ${log.status === 'present' ? 'Puntual' : log.status === 'late' ? 'Tardanza' : 'Ausente'}
          </span>
        </td>
        <td style="font-size: 0.75rem; color: var(--color-text-muted); max-width: 140px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;" title="${log.notes || ''}">
          ${log.notes || '-'}
        </td>
      </tr>
    `).join('');
  },

  updateStats(logs) {
    const todayStr = new Date().toISOString().split('T')[0];
    const todayLogs = logs.filter(l => l.date === todayStr);

    document.getElementById('stat-present').textContent = todayLogs.filter(l => l.status === 'present').length;
    document.getElementById('stat-late').textContent = todayLogs.filter(l => l.status === 'late').length;
    document.getElementById('stat-absent').textContent = todayLogs.filter(l => l.status === 'absent').length;
  },

  bindEvents(container) {
    const selectEmp = container.querySelector('#clock-emp-select');
    const btnIn = container.querySelector('#btn-clock-in');
    const btnOut = container.querySelector('#btn-clock-out');

    const todayStr = new Date().toISOString().split('T')[0];

    // MARCAJE ENTRADA
    if (btnIn) {
      btnIn.addEventListener('click', async () => {
        const empId = selectEmp.value;
        if (!empId) {
          alert('Por favor selecciona un colaborador.');
          return;
        }

        const employees = State.get('employees') || [];
        const emp = employees.find(e => e.id === empId);

        // Validar si ya marcó entrada hoy
        const logs = State.get('attendance') || [];
        const alreadyIn = logs.find(l => l.employee_id === empId && l.date === todayStr);
        if (alreadyIn) {
          alert('Este colaborador ya registró su entrada para el día de hoy.');
          return;
        }

        // Determinar estado: entrada estándar a las 08:00 AM en Panamá
        const now = new Date();
        const hour = now.getHours();
        const mins = now.getMinutes();
        const timeInStr = `${String(hour).padStart(2,'0')}:${String(mins).padStart(2,'0')}`;
        
        let status = 'present';
        let notes = '';

        if (hour > 8 || (hour === 8 && mins > 5)) { // 5 minutos de tolerancia
          status = 'late';
          notes = 'Marcaje tardío quincenal';
        }

        const record = {
          employee_id: empId,
          employee_name: `${emp.first_name} ${emp.last_name}`,
          department: emp.department,
          date: todayStr,
          time_in: timeInStr,
          time_out: null,
          status: status,
          notes: notes
        };

        const { error } = await DB.insert('attendance', record);
        if (!error) {
          alert(`Entrada registrada a las ${timeInStr} para ${emp.first_name} ${emp.last_name}.`);
          this.render(document.getElementById('app-content'));
        } else {
          alert('Error al registrar entrada: ' + error.message);
        }
      });
    }

    // MARCAJE SALIDA
    if (btnOut) {
      btnOut.addEventListener('click', async () => {
        const empId = selectEmp.value;
        if (!empId) {
          alert('Por favor selecciona un colaborador.');
          return;
        }

        const logs = State.get('attendance') || [];
        const activeLog = logs.find(l => l.employee_id === empId && l.date === todayStr);

        if (!activeLog) {
          alert('No se encontró registro de entrada para el día de hoy. Marcá entrada primero.');
          return;
        }

        if (activeLog.time_out) {
          alert('Este colaborador ya registró su salida para el día de hoy.');
          return;
        }

        const now = new Date();
        const timeOutStr = `${String(now.getHours()).padStart(2,'0')}:${String(now.getMinutes()).padStart(2,'0')}`;

        const { error } = await DB.update('attendance', activeLog.id, {
          time_out: timeOutStr
        });

        if (!error) {
          alert(`Salida registrada a las ${timeOutStr}.`);
          this.render(document.getElementById('app-content'));
        } else {
          alert('Error al registrar salida: ' + error.message);
        }
      });
    }

    // FILTROS DE HISTORIAL
    const searchAtt = container.querySelector('#search-att');
    const filterStatus = container.querySelector('#filter-att-status');

    const runFilters = () => {
      const query = searchAtt.value.toLowerCase().trim();
      const status = filterStatus.value;
      const logs = State.get('attendance') || [];

      const filtered = logs.filter(log => {
        const matchesName = (log.employee_name || '').toLowerCase().includes(query);
        const matchesStatus = !status || log.status === status;
        return matchesName && matchesStatus;
      });

      this.renderRows(filtered);
    };

    if (searchAtt) searchAtt.addEventListener('input', runFilters);
    if (filterStatus) filterStatus.addEventListener('change', runFilters);
  },

  showMiniLoader(container) {
    container.innerHTML = `
      <div style="display: flex; align-items: center; justify-content: center; min-height: 60vh;">
        <i class="fa-solid fa-circle-notch fa-spin" style="font-size: 2.5rem; color: var(--accent-primary);"></i>
      </div>
    `;
  }
};
