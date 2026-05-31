/**
 * =============================================================
 * DASHBOARD VIEW — HR ADMIN PANEL (PANAMÁ)
 * =============================================================
 * Panel de control principal. Analiza métricas en tiempo real,
 * despliega alertas críticas y renderiza visualizaciones
 * de analíticas avanzadas usando Chart.js.
 */

import { State } from '../state.js';
import { DB } from '../supabase.js';
import { Router } from '../router.js';

export default {
  async render(container) {
    // 1. Cargar datos actualizados desde la base de datos (Supabase o Demo)
    this.showMiniLoader(container);
    
    await Promise.all([
      DB.select('employees'),
      DB.select('attendance'),
      DB.select('leaveRequests'),
      DB.select('announcements')
    ]);

    // 2. Extraer colecciones del estado
    const employees = State.get('employees') || [];
    const attendance = State.get('attendance') || [];
    const leaveRequests = State.get('leaveRequests') || [];
    const announcements = State.get('announcements') || [];

    // 3. Cálculos de métricas en tiempo real
    const totalEmployees = employees.filter(e => e.status === 'active').length;
    
    // Asistencia de hoy (simulada sobre la fecha del último marcaje o fecha actual)
    const todayStr = new Date().toISOString().split('T')[0];
    const todaysLogs = attendance.filter(a => a.date === todayStr);
    const presentToday = todaysLogs.filter(l => l.status === 'present' || l.status === 'late').length;
    const attendancePct = totalEmployees > 0 ? Math.round((presentToday / totalEmployees) * 100) : 0;

    // Vacaciones activas
    const activeVacations = leaveRequests.filter(r => {
      const now = new Date();
      const start = new Date(r.start_date);
      const end = new Date(r.end_date);
      return r.status === 'approved' && now >= start && now <= end;
    }).length;

    // Cumpleaños de este mes
    const currentMonth = new Date().getMonth() + 1;
    const birthdaysThisMonth = employees.filter(e => {
      if (!e.birth_date) return false;
      const bMonth = new Date(e.birth_date).getMonth() + 1;
      return bMonth === currentMonth;
    }).length;

    // 4. Renderizar Maqueta HTML
    container.innerHTML = `
      <div class="page-header animate-fade-in">
        <div>
          <h1 class="page-title"><i class="fa-solid fa-chart-line"></i> Centro de Control</h1>
          <p class="page-subtitle">Bienvenido al Panel de Gestión Organizacional — Panamá.</p>
        </div>
      </div>

      <!-- METRIC CARDS GRID -->
      <div class="stats-grid animate-fade-in">
        
        <div class="glass-card stat-card">
          <div class="stat-info">
            <span class="stat-label">Colaboradores Activos</span>
            <span class="stat-value">${totalEmployees}</span>
          </div>
          <div class="stat-icon"><i class="fa-solid fa-user-tie"></i></div>
        </div>

        <div class="glass-card stat-card">
          <div class="stat-info">
            <span class="stat-label">Asistencia Hoy</span>
            <span class="stat-value">${attendancePct}%</span>
          </div>
          <div class="stat-icon"><i class="fa-solid fa-clipboard-user"></i></div>
        </div>

        <div class="glass-card stat-card">
          <div class="stat-info">
            <span class="stat-label">En Vacaciones</span>
            <span class="stat-value">${activeVacations}</span>
          </div>
          <div class="stat-icon"><i class="fa-solid fa-umbrella-beach"></i></div>
        </div>

        <div class="glass-card stat-card">
          <div class="stat-info">
            <span class="stat-label">Cumpleaños de ${new Date().toLocaleString('es-PA', { month: 'long' })}</span>
            <span class="stat-value">${birthdaysThisMonth}</span>
          </div>
          <div class="stat-icon"><i class="fa-solid fa-cake-candles"></i></div>
        </div>

      </div>

      <!-- MAIN CONTENT BLOCKS -->
      <div style="display: grid; grid-template-columns: 2fr 1.2fr; gap: 32px; align-items: start;" class="animate-fade-in">
        
        <!-- LEFT COLUMN: CHARTS & ANNOUNCEMENTS -->
        <div style="display: flex; flex-direction: column; gap: 32px;">
          
          <!-- ANALYTICS CARD -->
          <div class="glass-card">
            <h3 style="font-family: var(--font-title); font-weight: 700; margin-bottom: 20px; font-size: 1.15rem;">📊 Analítica de Salarios por Departamento</h3>
            <div style="position: relative; height: 280px; width: 100%;">
              <canvas id="chart-salaries"></canvas>
            </div>
          </div>

          <!-- RECENT ANNOUNCEMENTS -->
          <div class="glass-card">
            <h3 style="font-family: var(--font-title); font-weight: 700; margin-bottom: 20px; font-size: 1.15rem;">📢 Comunicados Oficiales</h3>
            <div style="display: flex; flex-direction: column; gap: 16px;">
              ${announcements.length > 0
                ? announcements.slice(0, 3).map(ann => `
                    <div style="border-left: 4px solid ${ann.priority === 'importante' ? 'var(--accent-danger)' : 'var(--accent-primary)'}; background: rgba(255,255,255,0.01); padding: 16px; border-radius: 0 10px 10px 0; border: 1px solid var(--border-glass); border-left-width: 4px;">
                      <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 6px;">
                        <h4 style="font-family: var(--font-title); font-weight: 700; font-size: 0.95rem;">${ann.title}</h4>
                        <span class="badge-custom ${ann.priority === 'importante' ? 'danger' : 'info'}">${ann.priority}</span>
                      </div>
                      <p style="font-size: 0.82rem; color: var(--color-text-secondary); line-height: 1.5; margin-bottom: 8px;">${ann.content}</p>
                      <span style="font-size: 0.7rem; color: var(--color-text-muted);"><i class="fa-solid fa-clock"></i> Publicado: ${new Date(ann.created_at).toLocaleDateString('es-PA')}</span>
                    </div>
                  `).join('')
                : `<p style="font-size: 0.88rem; color: var(--color-text-muted); text-align: center;">No hay comunicados publicados recientemente.</p>`
              }
            </div>
          </div>

        </div>

        <!-- RIGHT COLUMN: QUICK LINKS & ALERTS -->
        <div style="display: flex; flex-direction: column; gap: 32px;">
          
          <!-- QUICK LINKS CARD -->
          <div class="glass-card">
            <h3 style="font-family: var(--font-title); font-weight: 700; margin-bottom: 20px; font-size: 1.15rem;">⚡ Accesos Rápidos</h3>
            <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 12px;">
              <button class="glass-btn primary" onclick="window.location.hash='#/attendance'" style="padding: 16px 12px; flex-direction: column; justify-content: center; height: 90px; text-align: center; font-size: 0.78rem;">
                <i class="fa-solid fa-fingerprint" style="font-size: 1.5rem; margin-bottom: 6px;"></i> Marcaje
              </button>
              <button class="glass-btn" onclick="window.location.hash='#/payroll'" style="padding: 16px 12px; flex-direction: column; justify-content: center; height: 90px; text-align: center; font-size: 0.78rem;">
                <i class="fa-solid fa-file-invoice-dollar" style="font-size: 1.5rem; margin-bottom: 6px; color: var(--accent-secondary);"></i> Planillas
              </button>
              <button class="glass-btn" onclick="window.location.hash='#/employees'" style="padding: 16px 12px; flex-direction: column; justify-content: center; height: 90px; text-align: center; font-size: 0.78rem;">
                <i class="fa-solid fa-users" style="font-size: 1.5rem; margin-bottom: 6px; color: var(--accent-success);"></i> Personal
              </button>
              <button class="glass-btn" onclick="window.location.hash='#/vacations'" style="padding: 16px 12px; flex-direction: column; justify-content: center; height: 90px; text-align: center; font-size: 0.78rem;">
                <i class="fa-solid fa-umbrella-beach" style="font-size: 1.5rem; margin-bottom: 6px; color: var(--accent-warning);"></i> Vacaciones
              </button>
            </div>
          </div>

          <!-- SYSTEM ALERTS CENTER -->
          <div class="glass-card">
            <h3 style="font-family: var(--font-title); font-weight: 700; margin-bottom: 20px; font-size: 1.15rem;">⚠️ Alertas de Control</h3>
            <div style="display: flex; flex-direction: column; gap: 12px;" id="dashboard-alerts">
              <!-- Se inyectarán alertas basadas en vencimientos reales de contratos -->
            </div>
          </div>

        </div>

      </div>
    `;

    // 5. Generar gráficos interactivos y alertas dinámicas
    this.renderCharts(employees);
    this.renderAlerts(employees);
  },

  renderCharts(employees) {
    const canvas = document.getElementById('chart-salaries');
    if (!canvas) return;

    // Calcular salarios agregados por departamento
    const deptSalaries = {};
    employees.forEach(emp => {
      if (emp.status !== 'active') return;
      const dept = emp.department || 'Sin Depto';
      deptSalaries[dept] = (deptSalaries[dept] || 0) + Number(emp.salary);
    });

    const labels = Object.keys(deptSalaries);
    const data = Object.values(deptSalaries);

    // Instanciación del Chart
    new Chart(canvas, {
      type: 'bar',
      data: {
        labels: labels,
        datasets: [{
          label: 'Total Salarios ($ Balboas / USD)',
          data: data,
          backgroundColor: [
            'rgba(139, 92, 246, 0.55)', // Violet
            'rgba(6, 182, 212, 0.55)',  // Cyan
            'rgba(16, 185, 129, 0.55)', // Emerald
            'rgba(245, 158, 11, 0.55)', // Amber
            'rgba(239, 68, 68, 0.55)'   // Rose
          ],
          borderColor: [
            '#8b5cf6',
            '#06b6d4',
            '#10b981',
            '#f59e0b',
            '#ef4444'
          ],
          borderWidth: 1.5,
          borderRadius: 8
        }]
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        plugins: {
          legend: {
            display: false
          }
        },
        scales: {
          y: {
            grid: {
              color: 'rgba(255,255,255,0.05)'
            },
            ticks: {
              color: 'var(--color-text-secondary)',
              font: { size: 10 }
            }
          },
          x: {
            grid: {
              display: false
            },
            ticks: {
              color: 'var(--color-text-secondary)',
              font: { size: 10 }
            }
          }
        }
      }
    });
  },

  renderAlerts(employees) {
    const alertBox = document.getElementById('dashboard-alerts');
    if (!alertBox) return;

    const alerts = [];

    // Validar vencimientos de contratos o permisos
    employees.forEach(emp => {
      if (emp.status !== 'active') return;

      // Alerta de contratos temporales a expirar (< 30 días)
      if (emp.contract_expiry) {
        const expDate = new Date(emp.contract_expiry);
        const diff = Math.ceil((expDate - new Date()) / (1000 * 60 * 60 * 24));
        if (diff > 0 && diff <= 30) {
          alerts.push({
            type: 'warning',
            text: `El contrato temporal de <strong>${emp.first_name} ${emp.last_name}</strong> vence en ${diff} días (${emp.contract_expiry}).`
          });
        }
      }

      // Alerta de examen médico o permiso de trabajo a expirar
      if (emp.work_permit_expiry) {
        const expDate = new Date(emp.work_permit_expiry);
        const diff = Math.ceil((expDate - new Date()) / (1000 * 60 * 60 * 24));
        if (diff > 0 && diff <= 45) {
          alerts.push({
            type: 'danger',
            text: `Permiso de Trabajo extranjero de <strong>${emp.first_name}</strong> vence en ${diff} días.`
          });
        }
      }
    });

    if (alerts.length === 0) {
      alertBox.innerHTML = `
        <div style="background: rgba(16, 185, 129, 0.05); color: var(--accent-success); padding: 12px; border-radius: 8px; font-size: 0.8rem; text-align: center; border: 1px solid rgba(16,185,129,0.1);">
          <i class="fa-solid fa-circle-check"></i> El centro de control está limpio. Cero vencimientos próximos.
        </div>
      `;
    } else {
      alertBox.innerHTML = alerts.map(al => `
        <div style="background: ${al.type === 'danger' ? 'rgba(239, 68, 68, 0.08)' : 'rgba(245, 158, 11, 0.08)'}; color: ${al.type === 'danger' ? 'var(--accent-danger)' : 'var(--accent-warning)'}; border-left: 3px solid ${al.type === 'danger' ? 'var(--accent-danger)' : 'var(--accent-warning)'}; padding: 10px 14px; border-radius: 0 8px 8px 0; font-size: 0.78rem; line-height: 1.4;">
          <i class="fa-solid fa-triangle-exclamation"></i> ${al.text}
        </div>
      `).join('');
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
  // Integración dinámica de los módulos extra (definida más adelante)
};
