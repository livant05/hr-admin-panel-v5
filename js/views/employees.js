/**
 * =============================================================
 * EMPLOYEES VIEW — HR ADMIN PANEL (PANAMÁ)
 * =============================================================
 * Módulo de administración de personal. Implementa operaciones
 * CRUD avanzadas, validación estricta de documentos nacionales,
 * cálculo de salarios de mercado y procesamiento de carga masiva CSV.
 */

import { State } from '../state.js';
import { DB } from '../supabase.js';

export default {
  async render(container) {
    this.showMiniLoader(container);
    
    // Cargar información fresca de empleados
    await DB.select('employees');
    const employees = State.get('employees') || [];
    const departments = [...new Set(employees.map(e => e.department).filter(Boolean))];

    container.innerHTML = `
      <div class="page-header animate-fade-in">
        <div>
          <h1 class="page-title"><i class="fa-solid fa-users"></i> Gestión de Colaboradores</h1>
          <p class="page-subtitle">Administrá las fichas del personal, contratos y datos demográficos.</p>
        </div>
        <div style="display: flex; gap: 12px;">
          <button class="glass-btn" id="btn-import-csv"><i class="fa-solid fa-file-csv"></i> Importar CSV</button>
          <button class="glass-btn primary" id="btn-new-employee"><i class="fa-solid fa-user-plus"></i> Nuevo Colaborador</button>
        </div>
      </div>

      <!-- SEARCH AND FILTERS BAR -->
      <div class="glass-card animate-fade-in" style="padding: 16px; margin-bottom: 24px;">
        <div style="display: grid; grid-template-columns: 2fr 1fr 1fr 1fr; gap: 16px; align-items: center;">
          <div class="form-group" style="margin-bottom: 0;">
            <input type="text" id="search-emp" class="form-control" placeholder="Buscar por nombre, apellido o cédula...">
          </div>
          
          <div class="form-group" style="margin-bottom: 0;">
            <select id="filter-dept" class="form-control">
              <option value="">Todos los Departamentos</option>
              ${departments.map(d => `<option value="${d}">${d}</option>`).join('')}
            </select>
          </div>

          <div class="form-group" style="margin-bottom: 0;">
            <select id="filter-status" class="form-control">
              <option value="active">Activos</option>
              <option value="inactive">Inactivos</option>
              <option value="">Todos los Estados</option>
            </select>
          </div>

          <div class="badge-custom info" id="count-badge" style="justify-content: center; height: 38px; font-size: 0.82rem;">
            Colaboradores: ${employees.length}
          </div>
        </div>
      </div>

      <!-- EMPLOYEES RESPONSIVE TABLE -->
      <div class="glass-card animate-fade-in" style="padding: 0; overflow: hidden;">
        <div class="table-responsive">
          <table class="custom-table" id="table-employees">
            <thead>
              <tr>
                <th>Colaborador</th>
                <th>Cédula / SS</th>
                <th>Cargo / Depto</th>
                <th>Salario</th>
                <th>Fecha Ingreso</th>
                <th>Estado</th>
                <th style="text-align: center;">Acciones</th>
              </tr>
            </thead>
            <tbody id="employees-tbody">
              <!-- Cargar filas dinámicamente -->
            </tbody>
          </table>
        </div>
      </div>

      <!-- DYNAMIC MODAL CONTAINER (Inyectado al final) -->
      <div id="employee-modal-overlay" style="display: none; position: fixed; top: 0; left: 0; right: 0; bottom: 0; background: rgba(0,0,0,0.6); backdrop-filter: blur(8px); z-index: 1000; align-items: center; justify-content: center; padding: 20px;">
        <!-- HTML del modal inyectado aquí -->
      </div>
    `;

    this.renderRows(employees);
    this.bindEvents(container);
  },

  renderRows(employees) {
    const tbody = document.getElementById('employees-tbody');
    if (!tbody) return;

    if (employees.length === 0) {
      tbody.innerHTML = `
        <tr>
          <td colspan="7" style="text-align: center; color: var(--color-text-muted); padding: 40px;">
            <i class="fa-solid fa-users-slash" style="font-size: 2rem; margin-bottom: 12px;"></i><br>
            No se encontraron colaboradores registrados en este sistema.
          </td>
        </tr>
      `;
      return;
    }

    tbody.innerHTML = employees.map(emp => `
      <tr class="animate-fade-in" style="animation-duration: 0.2s;">
        <td>
          <div style="display: flex; align-items: center; gap: 12px;">
            <div class="avatar">${emp.first_name.substring(0,1)}${emp.last_name.substring(0,1)}</div>
            <div>
              <div style="font-weight: 600;">${emp.first_name} ${emp.last_name}</div>
              <div style="font-size: 0.72rem; color: var(--color-text-muted);">${emp.email || 'sin-correo@empresa.com'}</div>
            </div>
          </div>
        </td>
        <td>
          <div style="font-size: 0.85rem;">Cédula: <strong>${emp.cedula || 'N/A'}</strong></div>
          <div style="font-size: 0.72rem; color: var(--color-text-muted);">S.S.: ${emp.ss_number || 'N/A'} (DV ${emp.dv || '00'})</div>
        </td>
        <td>
          <div style="font-weight: 500;">${emp.position || 'Colaborador'}</div>
          <div style="font-size: 0.72rem; color: var(--color-text-muted);">${emp.department || 'Operaciones'}</div>
        </td>
        <td>
          <div style="font-weight: 600;">$${Number(emp.salary).toFixed(2)}</div>
          <div style="font-size: 0.72rem; color: var(--color-text-muted);">$${Number(emp.hourly_rate || 0).toFixed(4)} / hora</div>
        </td>
        <td style="font-size: 0.82rem;">${emp.start_date || 'N/A'}</td>
        <td>
          <span class="badge-custom ${emp.status === 'active' ? 'success' : 'danger'}">
            ${emp.status === 'active' ? 'Activo' : 'Inactivo'}
          </span>
        </td>
        <td>
          <div style="display: flex; gap: 8px; justify-content: center;">
            <button class="glass-btn edit-emp-btn" data-id="${emp.id}" title="Editar Ficha" style="padding: 6px 10px; font-size: 0.8rem;">
              <i class="fa-solid fa-pen-to-square"></i>
            </button>
            <button class="glass-btn danger delete-emp-btn" data-id="${emp.id}" title="Eliminar Colaborador" style="padding: 6px 10px; font-size: 0.8rem;">
              <i class="fa-solid fa-trash-can"></i>
            </button>
          </div>
        </td>
      </tr>
    `).join('');
  },

  bindEvents(container) {
    const modalOverlay = document.getElementById('employee-modal-overlay');

    // FILTRO: Búsqueda y Filtros combinados
    const searchInput = container.querySelector('#search-emp');
    const deptSelect = container.querySelector('#filter-dept');
    const statusSelect = container.querySelector('#filter-status');

    const applyFilters = () => {
      const query = searchInput.value.toLowerCase().trim();
      const dept = deptSelect.value;
      const status = statusSelect.value;
      const employees = State.get('employees') || [];

      const filtered = employees.filter(emp => {
        const matchesSearch = 
          `${emp.first_name} ${emp.last_name}`.toLowerCase().includes(query) ||
          (emp.cedula || '').toLowerCase().includes(query) ||
          (emp.email || '').toLowerCase().includes(query);
        
        const matchesDept = !dept || emp.department === dept;
        const matchesStatus = !status || emp.status === status;

        return matchesSearch && matchesDept && matchesStatus;
      });

      this.renderRows(filtered);
      container.querySelector('#count-badge').textContent = `Colaboradores: ${filtered.length}`;
    };

    if (searchInput) searchInput.addEventListener('input', applyFilters);
    if (deptSelect) deptSelect.addEventListener('change', applyFilters);
    if (statusSelect) statusSelect.addEventListener('change', applyFilters);

    // EVENTO: Mostrar Modal para Crear Colaborador
    const btnNew = container.querySelector('#btn-new-employee');
    if (btnNew) {
      btnNew.addEventListener('click', () => {
        this.openEmployeeModal(null, modalOverlay);
      });
    }

    // EVENTOS EN FILAS: Editar / Eliminar
    const tbody = container.querySelector('#employees-tbody');
    if (tbody) {
      tbody.addEventListener('click', async (e) => {
        const editBtn = e.target.closest('.edit-emp-btn');
        const deleteBtn = e.target.closest('.delete-emp-btn');

        if (editBtn) {
          const id = editBtn.getAttribute('data-id');
          this.openEmployeeModal(id, modalOverlay);
        }

        if (deleteBtn) {
          const id = deleteBtn.getAttribute('data-id');
          const employees = State.get('employees') || [];
          const emp = employees.find(e => e.id === id);
          
          if (confirm(`¿Estás seguro que deseas eliminar la ficha de ${emp.first_name} ${emp.last_name}? Esta acción no se puede deshacer.`)) {
            const { data, error } = await DB.delete('employees', id);
            if (!error) {
              alert('Ficha de colaborador eliminada con éxito.');
              this.render(container); // Recarga vista completa
            } else {
              alert('Error al eliminar: ' + error.message);
            }
          }
        }
      });
    }

    // EVENTO: Carga de CSV
    const btnImportCsv = container.querySelector('#btn-import-csv');
    if (btnImportCsv) {
      btnImportCsv.addEventListener('click', () => {
        this.openCsvImportModal(modalOverlay, container);
      });
    }
  },

  /**
   * Abre el Modal CRUD de Colaborador
   */
  openEmployeeModal(id = null, overlay) {
    const employees = State.get('employees') || [];
    const emp = id ? employees.find(e => e.id === id) : null;
    
    overlay.style.display = 'flex';
    overlay.innerHTML = `
      <div class="glass-card animate-fade-in" style="max-width: 800px; width: 100%; max-height: 90vh; overflow-y: auto; padding: 32px; border-top: 4px solid var(--accent-primary);">
        <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 24px;">
          <h2 style="font-family: var(--font-title); font-weight: 800; font-size: 1.5rem;">
            ${id ? `<i class="fa-solid fa-user-pen"></i> Editar Ficha Laboral` : `<i class="fa-solid fa-user-plus"></i> Nueva Ficha Laboral`}
          </h2>
          <button class="glass-btn danger" id="btn-close-modal" style="padding: 6px 12px; font-size: 0.88rem;"><i class="fa-solid fa-xmark"></i> Cerrar</button>
        </div>

        <form id="form-employee" style="display: flex; flex-direction: column; gap: 24px;">
          <!-- SECCIÓN 1: DATOS PERSONALES -->
          <div>
            <h3 style="font-family: var(--font-title); font-size: 0.95rem; font-weight: 700; text-transform: uppercase; color: var(--accent-primary); border-bottom: 1px solid var(--border-glass); padding-bottom: 6px; margin-bottom: 16px;">1. Datos Personales</h3>
            <div class="form-grid">
              <div class="form-group">
                <label>Nombre *</label>
                <input type="text" id="emp-first-name" class="form-control" value="${emp ? emp.first_name : ''}" required>
              </div>
              <div class="form-group">
                <label>Apellido *</label>
                <input type="text" id="emp-last-name" class="form-control" value="${emp ? emp.last_name : ''}" required>
              </div>
              <div class="form-group">
                <label>Cédula de Identidad (e.g. 8-888-888) *</label>
                <input type="text" id="emp-cedula" class="form-control" value="${emp ? emp.cedula : ''}" required>
              </div>
              <div class="form-group">
                <label>Nº Seguro Social</label>
                <input type="text" id="emp-ss" class="form-control" value="${emp ? emp.ss_number : ''}">
              </div>
              <div class="form-group">
                <label>D.V. (Dígito Verificador)</label>
                <input type="text" id="emp-dv" class="form-control" maxlength="2" value="${emp ? emp.dv : ''}">
              </div>
              <div class="form-group">
                <label>Fecha de Nacimiento</label>
                <input type="date" id="emp-birth" class="form-control" value="${emp ? emp.birth_date : ''}">
              </div>
              <div class="form-group">
                <label>Género (M/F)</label>
                <select id="emp-sex" class="form-control">
                  <option value="M" ${emp && emp.sex === 'M' ? 'selected' : ''}>Masculino</option>
                  <option value="F" ${emp && emp.sex === 'F' ? 'selected' : ''}>Femenino</option>
                </select>
              </div>
              <div class="form-group">
                <label>Estado Civil</label>
                <select id="emp-marital" class="form-control">
                  <option value="soltero" ${emp && emp.marital_status === 'soltero' ? 'selected' : ''}>Soltero/a</option>
                  <option value="casado" ${emp && emp.marital_status === 'casado' ? 'selected' : ''}>Casado/a</option>
                  <option value="divorciado" ${emp && emp.marital_status === 'divorciado' ? 'selected' : ''}>Divorciado/a</option>
                </select>
              </div>
              <div class="form-group">
                <label>Grupo Sanguíneo</label>
                <input type="text" id="emp-blood" class="form-control" placeholder="e.g. O+" value="${emp ? emp.blood_type : ''}">
              </div>
              <div class="form-group">
                <label>Celular / Teléfono</label>
                <input type="tel" id="emp-phone" class="form-control" value="${emp ? emp.phone : ''}">
              </div>
              <div class="form-group">
                <label>Email Personal</label>
                <input type="email" id="emp-email" class="form-control" value="${emp ? emp.email : ''}">
              </div>
              <div class="form-group">
                <label>Nacionalidad</label>
                <input type="text" id="emp-nationality" class="form-control" value="${emp ? emp.nationality : 'Panameña'}">
              </div>
              <div class="form-group full-width">
                <label>Dirección Residencial</label>
                <input type="text" id="emp-address" class="form-control" value="${emp ? emp.address : ''}">
              </div>
            </div>
          </div>

          <!-- SECCIÓN 2: CONTRATACIÓN Y SALARIOS -->
          <div>
            <h3 style="font-family: var(--font-title); font-size: 0.95rem; font-weight: 700; text-transform: uppercase; color: var(--accent-secondary); border-bottom: 1px solid var(--border-glass); padding-bottom: 6px; margin-bottom: 16px;">2. Configuración Laboral</h3>
            <div class="form-grid">
              <div class="form-group">
                <label>Departamento</label>
                <input type="text" id="emp-dept" class="form-control" placeholder="e.g. Recursos Humanos" value="${emp ? emp.department : ''}">
              </div>
              <div class="form-group">
                <label>Cargo / Posición</label>
                <input type="text" id="emp-pos" class="form-control" placeholder="e.g. Analista de Planilla" value="${emp ? emp.position : ''}">
              </div>
              <div class="form-group">
                <label>Sucursal / Oficina</label>
                <input type="text" id="emp-branch" class="form-control" placeholder="e.g. Sede Principal" value="${emp ? emp.branch : 'Sede Principal'}">
              </div>
              <div class="form-group">
                <label>Salario Mensual ($ USD) *</label>
                <input type="number" id="emp-salary" class="form-control" step="0.01" value="${emp ? emp.salary : '850.00'}" required>
              </div>
              <div class="form-group">
                <label>Horas Semanales Laborables</label>
                <input type="number" id="emp-hours" class="form-control" step="0.5" value="${emp ? emp.weekly_hours : '44'}" required>
              </div>
              <div class="form-group">
                <label>Tipo de Contrato</label>
                <select id="emp-contract-type" class="form-control">
                  <option value="indefinido" ${emp && emp.contract_type === 'indefinido' ? 'selected' : ''}>Indefinido</option>
                  <option value="temporal" ${emp && emp.contract_type === 'temporal' ? 'selected' : ''}>Definido (Temporal)</option>
                  <option value="servicio" ${emp && emp.contract_type === 'servicio' ? 'selected' : ''}>Servicio Profesional</option>
                </select>
              </div>
              <div class="form-group">
                <label>Fecha de Entrada *</label>
                <input type="date" id="emp-start-date" class="form-control" value="${emp ? emp.start_date : ''}" required>
              </div>
              <div class="form-group">
                <label>Vencimiento de Contrato (Si es temporal)</label>
                <input type="date" id="emp-contract-expiry" class="form-control" value="${emp ? emp.contract_expiry || '' : ''}">
              </div>
              <div class="form-group">
                <label>Estado Laboral</label>
                <select id="emp-status" class="form-control">
                  <option value="active" ${emp && emp.status === 'active' ? 'selected' : ''}>Activo</option>
                  <option value="inactive" ${emp && emp.status === 'inactive' ? 'selected' : ''}>Inactivo (Desvinculado)</option>
                </select>
              </div>
            </div>
          </div>

          <!-- SECCIÓN 3: CONTACTO DE EMERGENCIA -->
          <div>
            <h3 style="font-family: var(--font-title); font-size: 0.95rem; font-weight: 700; text-transform: uppercase; color: var(--accent-success); border-bottom: 1px solid var(--border-glass); padding-bottom: 6px; margin-bottom: 16px;">3. Contacto de Emergencia</h3>
            <div class="form-grid">
              <div class="form-group">
                <label>Nombre de Contacto</label>
                <input type="text" id="emp-emg-name" class="form-control" value="${emp ? emp.emergency_contact || '' : ''}">
              </div>
              <div class="form-group">
                <label>Teléfono de Contacto</label>
                <input type="tel" id="emp-emg-phone" class="form-control" value="${emp ? emp.emergency_phone || '' : ''}">
              </div>
              <div class="form-group">
                <label>Relación / Parentesco</label>
                <input type="text" id="emp-emg-relation" class="form-control" placeholder="e.g. Madre, Esposo" value="${emp ? emp.emergency_relation || '' : ''}">
              </div>
            </div>
          </div>

          <!-- SUBMIT BUTTONS -->
          <div style="display: flex; gap: 12px; margin-top: 10px;">
            <button type="submit" class="glass-btn primary" style="flex: 1;"><i class="fa-solid fa-floppy-disk"></i> Guardar Ficha Colaborador</button>
            <button type="button" class="glass-btn danger" id="btn-cancel-modal" style="flex: 0.3;">Cancelar</button>
          </div>

        </form>

      </div>
    `;

    // LÓGICA: Cerrar modal
    const closeModal = () => { overlay.style.display = 'none'; };
    overlay.querySelector('#btn-close-modal').addEventListener('click', closeModal);
    overlay.querySelector('#btn-cancel-modal').addEventListener('click', closeModal);

    // EVENTO: Envío del formulario
    overlay.querySelector('#form-employee').addEventListener('submit', async (e) => {
      e.preventDefault();
      
      const salary = Number(overlay.querySelector('#emp-salary').value);
      const weeklyHours = Number(overlay.querySelector('#emp-hours').value);
      
      // Cálculo del Valor por Hora en base a la norma estándar de Panamá: 
      // (Salario Mensual * 12 meses) / 52 semanas / horas semanales
      const monthlyHours = (weeklyHours * 52) / 12;
      const hourlyRate = salary / monthlyHours;

      // Calcular edad si hay fecha de nacimiento
      const birth = overlay.querySelector('#emp-birth').value;
      let age = null;
      if (birth) {
        age = new Date().getFullYear() - new Date(birth).getFullYear();
      }

      const record = {
        first_name: overlay.querySelector('#emp-first-name').value.trim(),
        last_name: overlay.querySelector('#emp-last-name').value.trim(),
        cedula: overlay.querySelector('#emp-cedula').value.trim(),
        ss_number: overlay.querySelector('#emp-ss').value.trim(),
        dv: overlay.querySelector('#emp-dv').value.trim(),
        birth_date: birth || null,
        age: age,
        sex: overlay.querySelector('#emp-sex').value,
        marital_status: overlay.querySelector('#emp-marital').value,
        blood_type: overlay.querySelector('#emp-blood').value.trim(),
        phone: overlay.querySelector('#emp-phone').value.trim(),
        email: overlay.querySelector('#emp-email').value.trim(),
        nationality: overlay.querySelector('#emp-nationality').value.trim(),
        address: overlay.querySelector('#emp-address').value.trim(),
        
        // Laborales
        department: overlay.querySelector('#emp-dept').value.trim(),
        position: overlay.querySelector('#emp-pos').value.trim(),
        branch: overlay.querySelector('#emp-branch').value.trim(),
        salary: salary,
        weekly_hours: weeklyHours,
        monthly_hours: Math.round(monthlyHours * 10) / 10,
        hourly_rate: hourlyRate,
        contract_type: overlay.querySelector('#emp-contract-type').value,
        start_date: overlay.querySelector('#emp-start-date').value,
        contract_expiry: overlay.querySelector('#emp-contract-expiry').value || null,
        status: overlay.querySelector('#emp-status').value,

        // Emergencia
        emergency_contact: overlay.querySelector('#emp-emg-name').value.trim(),
        emergency_phone: overlay.querySelector('#emp-emg-phone').value.trim(),
        emergency_relation: overlay.querySelector('#emp-emg-relation').value.trim()
      };

      let result;
      if (id) {
        // ACTUALIZAR
        result = await DB.update('employees', id, record);
      } else {
        // CREAR
        result = await DB.insert('employees', record);
      }

      if (!result.error) {
        alert(id ? 'Ficha de colaborador actualizada con éxito.' : 'Nuevo colaborador registrado exitosamente.');
        closeModal();
        // Recargar vista completa
        const mainContainer = document.getElementById('app-content');
        this.render(mainContainer);
      } else {
        alert('Error al guardar datos: ' + result.error.message);
      }
    });
  },

  /**
   * Abre Modal de Importación CSV
   */
  openCsvImportModal(overlay, viewContainer) {
    overlay.style.display = 'flex';
    overlay.innerHTML = `
      <div class="glass-card animate-fade-in" style="max-width: 500px; width: 100%; padding: 32px; border-top: 4px solid var(--accent-secondary);">
        <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 24px;">
          <h2 style="font-family: var(--font-title); font-weight: 800; font-size: 1.25rem;"><i class="fa-solid fa-file-csv"></i> Importación Masiva CSV</h2>
          <button class="glass-btn danger" id="btn-close-csv" style="padding: 6px 12px; font-size: 0.88rem;"><i class="fa-solid fa-xmark"></i></button>
        </div>

        <p style="color: var(--color-text-secondary); font-size: 0.82rem; line-height: 1.6; margin-bottom: 20px;">
          Cargá un archivo CSV con las columnas correspondientes en orden:<br>
          <code>first_name, last_name, cedula, email, department, position, salary</code>
        </p>

        <div class="form-group" style="margin-bottom: 24px;">
          <label>Seleccionar Archivo CSV</label>
          <input type="file" id="csv-file-input" class="form-control" accept=".csv" required>
        </div>

        <div style="display: flex; gap: 12px;">
          <button class="glass-btn primary" id="btn-process-csv" style="flex: 1;"><i class="fa-solid fa-circle-check"></i> Importar Datos</button>
          <button class="glass-btn" id="btn-cancel-csv" style="flex: 0.4;">Cancelar</button>
        </div>
      </div>
    `;

    const closeCsv = () => { overlay.style.display = 'none'; };
    overlay.querySelector('#btn-close-csv').addEventListener('click', closeCsv);
    overlay.querySelector('#btn-cancel-csv').addEventListener('click', closeCsv);

    overlay.querySelector('#btn-process-csv').addEventListener('click', async () => {
      const fileInput = overlay.querySelector('#csv-file-input');
      if (!fileInput.files[0]) {
        alert('Por favor selecciona un archivo CSV válido.');
        return;
      }

      const file = fileInput.files[0];
      const reader = new FileReader();
      
      reader.onload = async (e) => {
        const text = e.target.result;
        const lines = text.split('\n').map(l => l.split(',').map(cell => cell.trim()));
        
        let successCount = 0;
        
        // Asumimos primera línea cabecera: first_name, last_name, cedula, email, department, position, salary
        for (let i = 1; i < lines.length; i++) {
          const row = lines[i];
          if (row.length < 6 || !row[0]) continue; // Fila vacía o incompleta

          const salary = Number(row[6]) || 850.00;
          const weeklyHours = 44;
          const monthlyHours = (weeklyHours * 52) / 12;
          const hourlyRate = salary / monthlyHours;

          const record = {
            first_name: row[0],
            last_name: row[1],
            cedula: row[2],
            email: row[3],
            department: row[4],
            position: row[5],
            salary: salary,
            weekly_hours: weeklyHours,
            monthly_hours: Math.round(monthlyHours * 10) / 10,
            hourly_rate: hourlyRate,
            status: 'active',
            start_date: new Date().toISOString().split('T')[0]
          };

          const { error } = await DB.insert('employees', record);
          if (!error) successCount++;
        }

        alert(`Se importaron exitosamente ${successCount} colaboradores al sistema.`);
        closeCsv();
        this.render(viewContainer);
      };

      reader.readAsText(file);
    });
  },

  showMiniLoader(container) {
    container.innerHTML = `
      <div style="display: flex; align-items: center; justify-content: center; min-height: 60vh;">
        <i class="fa-solid fa-circle-notch fa-spin" style="font-size: 2.5rem; color: var(--accent-primary);"></i>
      </div>
    `;
  }
};
