/**
 * =============================================================
 * EXTRA MODULES VIEW CONTROLLER — HR PANEL (PANAMÁ)
 * =============================================================
 * Consolida la lógica e interfaces de los 6 módulos complementarios
 * del sistema. Respeta la legislación panameña y provee
 * interacciones y formatos de impresión especializados.
 */

import { State } from '../state.js';
import { DB } from '../supabase.js';

export async function renderExtraView(path, container) {
  console.log(`🔌 Cargando módulo extra dinámico: ${path}`);
  
  const session = State.get('session');
  
  if (path === 'medical-records') {
    await renderMedicalRecords(container, session);
  } else if (path === 'evaluations') {
    await renderEvaluations(container, session);
  } else if (path === 'uniforms') {
    await renderUniforms(container, session);
  } else if (path === 'trainings') {
    await renderTrainings(container, session);
  } else if (path === 'surveys') {
    await renderSurveys(container, session);
  } else if (path === 'announcements') {
    await renderAnnouncements(container, session);
  }
}

/**
 * =============================================================
 * 1. INCAPACIDADES MÉDICAS (Ley CSS Panamá)
 * =============================================================
 */
async function renderMedicalRecords(container, session) {
  await Promise.all([DB.select('employees'), DB.select('medicalRecords')]);
  const employees = State.get('employees') || [];
  const records = State.get('medicalRecords') || [];

  container.innerHTML = `
    <div class="page-header">
      <div>
        <h1 class="page-title"><i class="fa-solid fa-house-medical-flag"></i> Incapacidades Médicas CSS</h1>
        <p class="page-subtitle">Control de incapacidades bajo la Ley 51/2005 (CSS: 3 días empleador, a partir del 4º cubre 70% CSS).</p>
      </div>
      <button class="glass-btn primary" id="btn-new-medical"><i class="fa-solid fa-file-medical"></i> Registrar Incapacidad</button>
    </div>

    <div style="display: grid; grid-template-columns: 1fr; gap: 24px;">
      <div class="glass-card" style="padding: 0; overflow: hidden;">
        <div class="table-responsive">
          <table class="custom-table">
            <thead>
              <tr>
                <th>Colaborador</th>
                <th>Diagnóstico / Tipo</th>
                <th>Período</th>
                <th style="text-align: center;">Días Totales</th>
                <th style="text-align: center;">Días Empleador (100%)</th>
                <th style="text-align: center;">Días CSS (70%)</th>
                <th>Nº Certificado</th>
                <th>Estado</th>
              </tr>
            </thead>
            <tbody>
              ${records.map(rec => `
                <tr>
                  <td><strong>${rec.employee_name}</strong></td>
                  <td>
                    <div style="font-weight: 600;">${rec.diagnosis || 'No especificado'}</div>
                    <span style="font-size: 0.72rem; color: var(--color-text-muted);">${rec.type}</span>
                  </td>
                  <td style="font-size: 0.82rem;"><strong>${rec.start_date}</strong> a <strong>${rec.end_date}</strong></td>
                  <td style="text-align: center; font-weight: 700;">${rec.days}</td>
                  <td style="text-align: center; color: var(--accent-success); font-weight: 700;">${rec.employer_days}</td>
                  <td style="text-align: center; color: var(--accent-secondary); font-weight: 700;">${rec.css_days}</td>
                  <td style="font-family: monospace;">${rec.cert_number || 'N/A'}</td>
                  <td><span class="badge-custom success">Procesada</span></td>
                </tr>
              `).join('')}
            </tbody>
          </table>
        </div>
      </div>
    </div>

    <!-- Modal Form overlay -->
    <div id="extra-modal-overlay" style="display: none; position: fixed; top:0; left:0; right:0; bottom:0; background:rgba(0,0,0,0.6); backdrop-filter:blur(8px); z-index:1000; align-items:center; justify-content:center; padding:20px;"></div>
  `;

  // EVENTO: Nueva Incapacidad
  container.querySelector('#btn-new-medical').addEventListener('click', () => {
    const overlay = container.querySelector('#extra-modal-overlay');
    overlay.style.display = 'flex';
    overlay.innerHTML = `
      <div class="glass-card" style="max-width: 500px; width: 100%; border-top: 4px solid var(--accent-primary);">
        <h3 style="font-family: var(--font-title); font-weight: 800; font-size: 1.25rem; margin-bottom: 20px;">Registrar Incapacidad Médica</h3>
        <form id="form-medical" style="display: flex; flex-direction: column; gap: 16px;">
          <div class="form-group">
            <label>Colaborador *</label>
            <select id="med-emp" class="form-control" required>
              <option value="">Seleccione...</option>
              ${employees.filter(e => e.status === 'active').map(e => `<option value="${e.id}">${e.first_name} ${e.last_name}</option>`).join('')}
            </select>
          </div>
          <div class="form-grid" style="grid-template-columns: 1fr 1fr; margin-bottom:0;">
            <div class="form-group">
              <label>Desde *</label>
              <input type="date" id="med-start" class="form-control" required>
            </div>
            <div class="form-group">
              <label>Hasta *</label>
              <input type="date" id="med-end" class="form-control" required>
            </div>
          </div>
          <div class="form-group">
            <label>Diagnóstico *</label>
            <input type="text" id="med-diag" class="form-control" placeholder="e.g. Resfriado común, Fractura" required>
          </div>
          <div class="form-group">
            <label>Nº Certificado de CSS / Privado *</label>
            <input type="text" id="med-cert" class="form-control" placeholder="e.g. CSS-12345" required>
          </div>
          <div style="display: flex; gap: 12px; margin-top: 10px;">
            <button type="submit" class="glass-btn primary" style="flex: 1;">Guardar Incapacidad</button>
            <button type="button" class="glass-btn danger" id="btn-close-med" style="flex: 0.4;">Cancelar</button>
          </div>
        </form>
      </div>
    `;

    overlay.querySelector('#btn-close-med').addEventListener('click', () => overlay.style.display = 'none');

    overlay.querySelector('#form-medical').addEventListener('submit', async (e) => {
      e.preventDefault();
      const empId = overlay.querySelector('#med-emp').value;
      const start = overlay.querySelector('#med-start').value;
      const end = overlay.querySelector('#med-end').value;
      const diag = overlay.querySelector('#med-diag').value.trim();
      const cert = overlay.querySelector('#med-cert').value.trim();

      const diff = new Date(end) - new Date(start);
      const totalDays = Math.ceil(diff / (1000 * 60 * 60 * 24)) + 1;

      if (totalDays <= 0) {
        alert('Fecha de finalización debe ser posterior a la de inicio.');
        return;
      }

      // Distribución de Ley CSS en Panamá
      const employerDays = Math.min(totalDays, 3); // Empleador paga primeros 3 días al 100%
      const cssDays = Math.max(totalDays - 3, 0);   // CSS cubre a partir del día 4

      const emp = employees.find(e => e.id === empId);

      const record = {
        employee_id: empId,
        employee_name: `${emp.first_name} ${emp.last_name}`,
        type: 'Incapacidad médica',
        diagnosis: diag,
        start_date: start,
        end_date: end,
        days: totalDays,
        employer_days: employerDays,
        css_days: cssDays,
        cert_number: cert,
        status: 'activa'
      };

      const { error } = await DB.insert('medicalRecords', record);
      if (!error) {
        alert('Incapacidad guardada exitosamente.');
        overlay.style.display = 'none';
        renderMedicalRecords(container, session);
      } else {
        alert('Error al guardar: ' + error.message);
      }
    });
  });
}

/**
 * =============================================================
 * 2. EVALUACIONES DE DESEMPEÑO
 * =============================================================
 */
async function renderEvaluations(container, session) {
  await Promise.all([DB.select('employees'), DB.select('evaluations')]);
  const employees = State.get('employees') || [];
  const evaluations = State.get('evaluations') || [];

  container.innerHTML = `
    <div class="page-header">
      <div>
        <h1 class="page-title"><i class="fa-solid fa-ranking-star"></i> Evaluaciones de Desempeño</h1>
        <p class="page-subtitle">Evaluaciones trimestrales y anuales basadas en 4 criterios de competencia.</p>
      </div>
      <button class="glass-btn primary" id="btn-new-eval"><i class="fa-solid fa-clipboard-question"></i> Evaluar Colaborador</button>
    </div>

    <div class="glass-card" style="padding: 0; overflow: hidden;">
      <div class="table-responsive">
        <table class="custom-table">
          <thead>
            <tr>
              <th>Colaborador</th>
              <th>Período / Evaluador</th>
              <th>Calificaciones (1 a 5)</th>
              <th style="text-align: center;">Promedio</th>
              <th>Categoría</th>
              <th>Comentarios</th>
            </tr>
          </thead>
          <tbody>
            ${evaluations.map(ev => `
              <tr>
                <td><strong>${ev.employee_name}</strong></td>
                <td>
                  <div style="font-weight: 600;">${ev.period}</div>
                  <span style="font-size: 0.72rem; color: var(--color-text-muted);">Por: ${ev.evaluator}</span>
                </td>
                <td style="font-size: 0.75rem; color: var(--color-text-secondary);">
                  Puntualidad: <strong>${ev.scores.Puntualidad || 5}</strong> · 
                  Trabajo en Equipo: <strong>${ev.scores['Trabajo en Equipo'] || 5}</strong> · 
                  Productividad: <strong>${ev.scores.Productividad || 5}</strong>
                </td>
                <td style="text-align: center; font-weight: 700; color: var(--accent-secondary); font-size: 1.1rem;">${ev.avg}</td>
                <td>
                  <span class="badge-custom ${ev.avg >= 4 ? 'success' : ev.avg >= 3 ? 'warning' : 'danger'}">
                    ${ev.category}
                  </span>
                </td>
                <td style="font-size: 0.78rem; max-width: 200px; text-overflow: ellipsis; white-space: nowrap; overflow: hidden;" title="${ev.comments || ''}">${ev.comments || '-'}</td>
              </tr>
            `).join('')}
          </tbody>
        </table>
      </div>
    </div>

    <div id="extra-modal-overlay" style="display: none; position: fixed; top:0; left:0; right:0; bottom:0; background:rgba(0,0,0,0.6); backdrop-filter:blur(8px); z-index:1000; align-items:center; justify-content:center; padding:20px;"></div>
  `;

  // EVENTO: Nueva Evaluación
  container.querySelector('#btn-new-eval').addEventListener('click', () => {
    const overlay = container.querySelector('#extra-modal-overlay');
    overlay.style.display = 'flex';
    overlay.innerHTML = `
      <div class="glass-card" style="max-width: 500px; width: 100%; border-top: 4px solid var(--accent-primary); max-height: 90vh; overflow-y: auto;">
        <h3 style="font-family: var(--font-title); font-weight: 800; font-size: 1.25rem; margin-bottom: 20px;">Formulario de Evaluación</h3>
        <form id="form-eval" style="display: flex; flex-direction: column; gap: 16px;">
          <div class="form-group">
            <label>Colaborador *</label>
            <select id="ev-emp" class="form-control" required>
              <option value="">Seleccione...</option>
              ${employees.filter(e => e.status === 'active').map(e => `<option value="${e.id}">${e.first_name} ${e.last_name}</option>`).join('')}
            </select>
          </div>
          <div class="form-group">
            <label>Período *</label>
            <input type="text" id="ev-period" class="form-control" placeholder="e.g. Segundo Trimestre 2026" required>
          </div>
          <div class="form-grid" style="grid-template-columns: 1fr 1fr; margin-bottom: 0;">
            <div class="form-group">
              <label>Puntualidad (1 a 5) *</label>
              <input type="number" id="sc-punt" class="form-control" min="1" max="5" value="5" required>
            </div>
            <div class="form-group">
              <label>Trabajo en Equipo (1 a 5) *</label>
              <input type="number" id="sc-team" class="form-control" min="1" max="5" value="5" required>
            </div>
            <div class="form-group">
              <label>Productividad (1 a 5) *</label>
              <input type="number" id="sc-prod" class="form-control" min="1" max="5" value="5" required>
            </div>
            <div class="form-group">
              <label>Liderazgo (1 a 5) *</label>
              <input type="number" id="sc-lid" class="form-control" min="1" max="5" value="5" required>
            </div>
          </div>
          <div class="form-group">
            <label>Comentarios Generales</label>
            <textarea id="ev-comm" class="form-control" rows="2"></textarea>
          </div>
          <div style="display: flex; gap: 12px; margin-top: 10px;">
            <button type="submit" class="glass-btn primary" style="flex: 1;">Finalizar Evaluación</button>
            <button type="button" class="glass-btn danger" id="btn-close-eval" style="flex: 0.4;">Cancelar</button>
          </div>
        </form>
      </div>
    `;

    overlay.querySelector('#btn-close-eval').addEventListener('click', () => overlay.style.display = 'none');

    overlay.querySelector('#form-eval').addEventListener('submit', async (e) => {
      e.preventDefault();
      const empId = overlay.querySelector('#ev-emp').value;
      const period = overlay.querySelector('#ev-period').value.trim();
      const p1 = Number(overlay.querySelector('#sc-punt').value);
      const p2 = Number(overlay.querySelector('#sc-team').value);
      const p3 = Number(overlay.querySelector('#sc-prod').value);
      const p4 = Number(overlay.querySelector('#sc-lid').value);
      const comm = overlay.querySelector('#ev-comm').value.trim();

      const avg = (p1 + p2 + p3 + p4) / 4;
      let category = 'Aceptable';
      if (avg >= 4.5) category = 'Excelente';
      else if (avg >= 4.0) category = 'Sobresaliente';
      else if (avg >= 3.0) category = 'Aceptable';
      else category = 'Deficiente';

      const emp = employees.find(e => e.id === empId);

      const record = {
        employee_id: empId,
        employee_name: `${emp.first_name} ${emp.last_name}`,
        period: period,
        evaluator: session ? session.name : 'Administrador',
        scores: {
          'Puntualidad': p1,
          'Trabajo en Equipo': p2,
          'Productividad': p3,
          'Liderazgo': p4
        },
        avg: avg,
        category: category,
        comments: comm,
        status: 'completada'
      };

      const { error } = await DB.insert('evaluations', record);
      if (!error) {
        alert('Evaluación registrada con éxito.');
        overlay.style.display = 'none';
        renderEvaluations(container, session);
      } else {
        alert('Error al guardar: ' + error.message);
      }
    });
  });
}

/**
 * =============================================================
 * 3. UNIFORMES / EQUIPOS
 * =============================================================
 */
async function renderUniforms(container, session) {
  await Promise.all([DB.select('employees'), DB.select('uniforms')]);
  const employees = State.get('employees') || [];
  const uniforms = State.get('uniforms') || [];

  container.innerHTML = `
    <div class="page-header">
      <div>
        <h1 class="page-title"><i class="fa-solid fa-shirt"></i> Control de Uniformes y Equipos</h1>
        <p class="page-subtitle">Inventario y control de asignación de equipos de protección e indumentaria laboral.</p>
      </div>
      <button class="glass-btn primary" id="btn-new-uniform"><i class="fa-solid fa-folder-plus"></i> Asignar Equipo</button>
    </div>

    <div class="glass-card" style="padding: 0; overflow: hidden;">
      <div class="table-responsive">
        <table class="custom-table">
          <thead>
            <tr>
              <th>Colaborador</th>
              <th>Artículo Asignado</th>
              <th>Talla / Medida</th>
              <th style="text-align: center;">Cantidad</th>
              <th>Fecha de Entrega</th>
              <th>Valor Unitario</th>
              <th>Estado</th>
            </tr>
          </thead>
          <tbody>
            ${uniforms.map(uni => `
              <tr>
                <td><strong>${uni.employee_name}</strong></td>
                <td>
                  <div style="font-weight: 600;">${uni.item}</div>
                  <span style="font-size: 0.72rem; color: var(--color-text-muted);">${uni.category}</span>
                </td>
                <td style="font-weight: 600;">${uni.size || 'Única'}</td>
                <td style="text-align: center; font-weight: 700;">${uni.quantity}</td>
                <td>${uni.date}</td>
                <td>$${Number(uni.value || 0).toFixed(2)}</td>
                <td>
                  <span class="badge-custom success">Vigente</span>
                </td>
              </tr>
            `).join('')}
          </tbody>
        </table>
      </div>
    </div>

    <div id="extra-modal-overlay" style="display: none; position: fixed; top:0; left:0; right:0; bottom:0; background:rgba(0,0,0,0.6); backdrop-filter:blur(8px); z-index:1000; align-items:center; justify-content:center; padding:20px;"></div>
  `;

  // EVENTO: Asignar Uniforme
  container.querySelector('#btn-new-uniform').addEventListener('click', () => {
    const overlay = container.querySelector('#extra-modal-overlay');
    overlay.style.display = 'flex';
    overlay.innerHTML = `
      <div class="glass-card" style="max-width: 480px; width: 100%; border-top: 4px solid var(--accent-primary);">
        <h3 style="font-family: var(--font-title); font-weight: 800; font-size: 1.25rem; margin-bottom: 20px;">Asignar Uniforme o EPP</h3>
        <form id="form-uniform" style="display: flex; flex-direction: column; gap: 16px;">
          <div class="form-group">
            <label>Colaborador *</label>
            <select id="uni-emp" class="form-control" required>
              <option value="">Seleccione...</option>
              ${employees.filter(e => e.status === 'active').map(e => `<option value="${e.id}">${e.first_name} ${e.last_name}</option>`).join('')}
            </select>
          </div>
          <div class="form-group">
            <label>Artículo *</label>
            <input type="text" id="uni-item" class="form-control" placeholder="e.g. Camisa Polo Azul, Casco" required>
          </div>
          <div class="form-grid" style="grid-template-columns: 1fr 1fr; margin-bottom:0;">
            <div class="form-group">
              <label>Categoría</label>
              <input type="text" id="uni-cat" class="form-control" placeholder="e.g. Uniforme, EPP">
            </div>
            <div class="form-group">
              <label>Talla</label>
              <input type="text" id="uni-size" class="form-control" placeholder="e.g. M, L, 41">
            </div>
          </div>
          <div class="form-grid" style="grid-template-columns: 1fr 1fr; margin-bottom:0;">
            <div class="form-group">
              <label>Cantidad</label>
              <input type="number" id="uni-qty" class="form-control" min="1" value="1" required>
            </div>
            <div class="form-group">
              <label>Valor Unitario ($)</label>
              <input type="number" id="uni-val" class="form-control" step="0.01" value="0.00">
            </div>
          </div>
          <div style="display: flex; gap: 12px; margin-top: 10px;">
            <button type="submit" class="glass-btn primary" style="flex: 1;">Registrar Asignación</button>
            <button type="button" class="glass-btn danger" id="btn-close-uni" style="flex: 0.4;">Cancelar</button>
          </div>
        </form>
      </div>
    `;

    overlay.querySelector('#btn-close-uni').addEventListener('click', () => overlay.style.display = 'none');

    overlay.querySelector('#form-uniform').addEventListener('submit', async (e) => {
      e.preventDefault();
      const empId = overlay.querySelector('#uni-emp').value;
      const item = overlay.querySelector('#uni-item').value.trim();
      const cat = overlay.querySelector('#uni-cat').value.trim();
      const size = overlay.querySelector('#uni-size').value.trim();
      const qty = Number(overlay.querySelector('#uni-qty').value);
      const val = Number(overlay.querySelector('#uni-val').value);

      const emp = employees.find(e => e.id === empId);

      const record = {
        employee_id: empId,
        employee_name: `${emp.first_name} ${emp.last_name}`,
        item: item,
        category: cat,
        size: size,
        quantity: qty,
        date: new Date().toISOString().split('T')[0],
        value: val,
        status: 'activo'
      };

      const { error } = await DB.insert('uniforms', record);
      if (!error) {
        alert('Asignación de equipo registrada exitosamente.');
        overlay.style.display = 'none';
        renderUniforms(container, session);
      } else {
        alert('Error al registrar: ' + error.message);
      }
    });
  });
}

/**
 * =============================================================
 * 4. CAPACITACIONES (Diplomas imprimibles)
 * =============================================================
 */
async function renderTrainings(container, session) {
  await Promise.all([
    DB.select('employees'),
    DB.select('trainings'),
    DB.select('enrollments')
  ]);
  const employees = State.get('employees') || [];
  const courses = State.get('trainings') || [];
  const enrollments = State.get('enrollments') || [];

  container.innerHTML = `
    <div class="page-header">
      <div>
        <h1 class="page-title"><i class="fa-solid fa-graduation-cap"></i> Plan de Capacitaciones</h1>
        <p class="page-subtitle">Gestión de cursos corporativos, inscripciones y generación de diplomas imprimibles.</p>
      </div>
      <button class="glass-btn primary" id="btn-new-course"><i class="fa-solid fa-plus"></i> Crear Curso</button>
    </div>

    <div style="display: grid; grid-template-columns: 1.1fr 2fr; gap: 32px; align-items: start;">
      <!-- FORM DE REGISTRO E INSCRIPCIÓN -->
      <div style="display: flex; flex-direction: column; gap: 24px;">
        <div class="glass-card" style="border-top: 3px solid var(--accent-secondary);">
          <h3 style="font-family: var(--font-title); font-weight: 700; margin-bottom: 18px; font-size: 1.1rem;">Inscribir Colaborador</h3>
          <form id="form-enroll" style="display: flex; flex-direction: column; gap: 14px;">
            <div class="form-group">
              <label>Seleccionar Curso</label>
              <select id="enr-course" class="form-control" required>
                <option value="">Seleccione...</option>
                ${courses.map(c => `<option value="${c.id}">${c.name}</option>`).join('')}
              </select>
            </div>
            <div class="form-group">
              <label>Colaborador</label>
              <select id="enr-emp" class="form-control" required>
                <option value="">Seleccione...</option>
                ${employees.filter(e => e.status === 'active').map(e => `<option value="${e.id}">${e.first_name} ${e.last_name}</option>`).join('')}
              </select>
            </div>
            <button type="submit" class="glass-btn primary" style="width: 100%; margin-top: 10px;"><i class="fa-solid fa-user-check"></i> Confirmar Matrícula</button>
          </form>
        </div>
      </div>

      <!-- LISTA DE MATRICULADOS Y DIPLOMAS -->
      <div class="glass-card" style="padding: 0; overflow: hidden;">
        <div style="padding: 24px;">
          <h3 style="font-family: var(--font-title); font-weight: 700; font-size: 1.1rem; margin-bottom: 4px;">Inscripciones y Diplomas</h3>
        </div>
        <div class="table-responsive">
          <table class="custom-table">
            <thead>
              <tr>
                <th>Curso / Horas</th>
                <th>Colaborador</th>
                <th>Estado</th>
                <th style="text-align: center;">Acciones</th>
              </tr>
            </thead>
            <tbody>
              ${enrollments.map(enr => `
                <tr>
                  <td>
                    <div style="font-weight: 600;">${enr.training_name}</div>
                  </td>
                  <td><strong>${enr.employee_name}</strong></td>
                  <td><span class="badge-custom success">${enr.status}</span></td>
                  <td style="text-align: center;">
                    <button class="glass-btn btn-print-cert" 
                      data-emp="${enr.employee_name}"
                      data-course="${enr.training_name}"
                      style="padding: 6px 12px; font-size: 0.78rem;">
                      <i class="fa-solid fa-award"></i> Diploma
                    </button>
                  </td>
                </tr>
              `).join('')}
            </tbody>
          </table>
        </div>
      </div>
    </div>

    <div id="extra-modal-overlay" style="display: none; position: fixed; top:0; left:0; right:0; bottom:0; background:rgba(0,0,0,0.6); backdrop-filter:blur(8px); z-index:1000; align-items:center; justify-content:center; padding:20px;"></div>
  `;

  // EVENTO: Registrar Matrícula
  container.querySelector('#form-enroll').addEventListener('submit', async (e) => {
    e.preventDefault();
    const courseId = container.querySelector('#enr-course').value;
    const empId = container.querySelector('#enr-emp').value;

    const course = courses.find(c => c.id === courseId);
    const emp = employees.find(e => e.id === empId);

    const record = {
      training_id: courseId,
      training_name: course.name,
      employee_id: empId,
      employee_name: `${emp.first_name} ${emp.last_name}`,
      status: 'completado'
    };

    const { error } = await DB.insert('enrollments', record);
    if (!error) {
      alert('Matrícula registrada y calificada como completada.');
      renderTrainings(container, session);
    } else {
      alert('Error: ' + error.message);
    }
  });

  // EVENTO: Mostrar Diploma Imprimible
  const overlay = container.querySelector('#extra-modal-overlay');
  container.querySelectorAll('.btn-print-cert').forEach(btn => {
    btn.addEventListener('click', () => {
      const d = btn.dataset;
      overlay.style.display = 'flex';
      overlay.innerHTML = `
        <div class="glass-card animate-fade-in" style="max-width: 650px; width: 100%; background: #ffffff; color: #111; padding: 36px;">
          <!-- DIPLOMA CONTAINER -->
          <div id="printable-diploma" style="border: 6px double #c5a880; background: #faf8f5; padding: 30px; text-align: center; font-family: 'Georgia', serif; position: relative;">
            <div style="border: 2px solid #c5a880; padding: 24px;">
              <h2 style="font-size: 1.5rem; letter-spacing: 2px; color: #8a6d3b; text-transform: uppercase; margin-bottom: 20px; font-family: 'Outfit', sans-serif; font-weight: 800;">DIPLOMA DE PARTICIPACIÓN</h2>
              <p style="font-size: 0.88rem; font-style: italic; color: #555; margin-bottom: 12px;">Se otorga con orgullo el presente reconocimiento a:</p>
              <h1 style="font-size: 2.25rem; font-weight: 700; color: #1a1a2e; margin-bottom: 20px;">${d.emp}</h1>
              <p style="font-size: 0.88rem; color: #555; line-height: 1.6; margin-bottom: 24px; max-width: 460px; margin-left: auto; margin-right: auto;">
                Por haber asistido y completado satisfactoriamente el entrenamiento de desarrollo profesional en el curso:<br>
                <strong style="font-size: 1.1rem; color: #8a6d3b; font-family: 'Outfit', sans-serif;">${d.course}</strong>
              </p>
              <div style="border-top: 1px solid #c5a880; width: 180px; margin: 40px auto 0; padding-top: 8px; font-size: 0.72rem; font-family: 'Inter', sans-serif; color: #666;">
                Dirección de Capacitación Corporativa
              </div>
            </div>
          </div>
          
          <div style="display: flex; gap: 12px; margin-top: 24px;">
            <button class="glass-btn primary" id="btn-print-dip-action" style="flex: 1;"><i class="fa-solid fa-print"></i> Imprimir Diploma</button>
            <button class="glass-btn danger" id="btn-close-dip" style="flex: 0.4;">Cerrar</button>
          </div>
        </div>
      `;

      overlay.querySelector('#btn-close-dip').addEventListener('click', () => overlay.style.display = 'none');
      
      overlay.querySelector('#btn-print-dip-action').addEventListener('click', () => {
        const printContent = document.getElementById('printable-diploma').outerHTML;
        document.body.innerHTML = printContent;
        window.print();
        window.location.reload();
      });
    });
  });

  // EVENTO: Crear Curso
  container.querySelector('#btn-new-course').addEventListener('click', () => {
    overlay.style.display = 'flex';
    overlay.innerHTML = `
      <div class="glass-card" style="max-width: 480px; width: 100%; border-top: 4px solid var(--accent-primary);">
        <h3 style="font-family: var(--font-title); font-weight: 800; font-size: 1.25rem; margin-bottom: 20px;">Crear Nuevo Curso</h3>
        <form id="form-course" style="display: flex; flex-direction: column; gap: 16px;">
          <div class="form-group">
            <label>Nombre del Curso *</label>
            <input type="text" id="crs-name" class="form-control" required>
          </div>
          <div class="form-grid" style="grid-template-columns: 1fr 1fr; margin-bottom: 0;">
            <div class="form-group">
              <label>Categoría</label>
              <input type="text" id="crs-cat" class="form-control" placeholder="e.g. Legal, Ventas">
            </div>
            <div class="form-group">
              <label>Horas Académicas</label>
              <input type="number" id="crs-hours" class="form-control" min="1" value="8">
            </div>
          </div>
          <div style="display: flex; gap: 12px; margin-top: 10px;">
            <button type="submit" class="glass-btn primary" style="flex: 1;">Guardar Curso</button>
            <button type="button" class="glass-btn danger" id="btn-close-crs" style="flex: 0.4;">Cancelar</button>
          </div>
        </form>
      </div>
    `;

    overlay.querySelector('#btn-close-crs').addEventListener('click', () => overlay.style.display = 'none');

    overlay.querySelector('#form-course').addEventListener('submit', async (e) => {
      e.preventDefault();
      const name = overlay.querySelector('#crs-name').value.trim();
      const cat = overlay.querySelector('#crs-cat').value.trim();
      const hrs = Number(overlay.querySelector('#crs-hours').value);

      const record = {
        name,
        category: cat,
        hours: hrs,
        status: 'activo'
      };

      const { error } = await DB.insert('trainings', record);
      if (!error) {
        alert('Curso creado con éxito.');
        overlay.style.display = 'none';
        renderTrainings(container, session);
      } else {
        alert('Error: ' + error.message);
      }
    });
  });
}

/**
 * =============================================================
 * 5. ENCUESTAS (Clima Laboral con estadísticas)
 * =============================================================
 */
async function renderSurveys(container, session) {
  await Promise.all([DB.select('surveys'), DB.select('surveyResponses')]);
  const surveys = State.get('surveys') || [];
  const responses = State.get('surveyResponses') || [];

  container.innerHTML = `
    <div class="page-header">
      <div>
        <h1 class="page-title"><i class="fa-solid fa-square-poll-horizontal"></i> Encuestas de Clima Laboral</h1>
        <p class="page-subtitle">Medición y estadísticas sobre el clima, relaciones y satisfacción en la empresa.</p>
      </div>
    </div>

    <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 32px;">
      <!-- RESPONDER ENCUESTA ACTIVA -->
      <div class="glass-card" style="border-top: 3px solid var(--accent-primary);">
        <h3 style="font-family: var(--font-title); font-weight: 700; font-size: 1.1rem; margin-bottom: 20px;">Participar en Encuesta Activa</h3>
        ${surveys.length > 0
          ? `
            <form id="form-fill-survey" style="display: flex; flex-direction: column; gap: 16px;">
              <h4 style="font-family: var(--font-title); font-weight: 700; color: var(--accent-secondary); font-size: 0.95rem;">${surveys[0].title}</h4>
              <p style="font-size: 0.78rem; color: var(--color-text-secondary); margin-bottom: 10px;">${surveys[0].description}</p>
              
              <div class="form-group">
                <label>1. ¿Te sentís valorado en la empresa? (Escala 1 a 5) *</label>
                <input type="number" id="ans-q1" class="form-control" min="1" max="5" value="5" required>
              </div>

              <div class="form-group">
                <label>2. ¿Tenés las herramientas necesarias para tu labor? *</label>
                <select id="ans-q2" class="form-control" required>
                  <option value="Si">Sí, completamente</option>
                  <option value="No">No, requiere mejoras</option>
                </select>
              </div>

              <div class="form-group">
                <label>3. ¿Qué sugerencia tenés para mejorar la oficina? *</label>
                <textarea id="ans-q3" class="form-control" rows="2" placeholder="Tus sugerencias ayudan mucho..." required></textarea>
              </div>

              <button type="submit" class="glass-btn primary" style="width: 100%; margin-top: 10px;"><i class="fa-solid fa-paper-plane"></i> Enviar Respuestas Anónimas</button>
            </form>
            `
          : `<p style="font-size: 0.88rem; color: var(--color-text-muted); text-align: center;">No hay encuestas activas en este período.</p>`
        }
      </div>

      <!-- ESTADÍSTICAS EN TIEMPO REAL -->
      <div class="glass-card">
        <h3 style="font-family: var(--font-title); font-weight: 700; font-size: 1.1rem; margin-bottom: 16px;">Estadísticas Generales</h3>
        <div style="background: rgba(255,255,255,0.01); border: 1px solid var(--border-glass); border-radius: 12px; padding: 20px; display: flex; flex-direction: column; gap: 16px;">
          <div>
            <div style="display: flex; justify-content: space-between; font-size: 0.85rem; margin-bottom: 6px;">
              <span>Total respuestas recibidas:</span>
              <strong>${responses.length}</strong>
            </div>
            <div style="height: 6px; background: var(--bg-input); border-radius: 6px; overflow: hidden;">
              <div style="width: ${Math.min(responses.length * 20, 100)}%; height: 100%; background: var(--accent-gradient);"></div>
            </div>
          </div>

          <!-- Promedio escala -->
          <div style="display: flex; justify-content: space-between; align-items: center; border-top: 1px solid var(--border-glass); padding-top: 12px;">
            <span style="font-size: 0.85rem; color: var(--color-text-secondary);">Índice de Aceptación Promedio (Escala):</span>
            <strong style="font-size: 1.5rem; color: var(--accent-warning);">
              ${responses.length > 0 
                ? (responses.reduce((sum, r) => sum + Number(r.answers.q1 || 5), 0) / responses.length).toFixed(2)
                : '0.00'
              } / 5.0
            </strong>
          </div>
        </div>
      </div>
    </div>
  `;

  // EVENTO: Responder Encuesta
  const formFill = container.querySelector('#form-fill-survey');
  if (formFill) {
    formFill.addEventListener('submit', async (e) => {
      e.preventDefault();
      const q1 = Number(container.querySelector('#ans-q1').value);
      const q2 = container.querySelector('#ans-q2').value;
      const q3 = container.querySelector('#ans-q3').value.trim();

      const record = {
        survey_id: surveys[0].id,
        answers: {
          'q1': q1,
          'q2': q2,
          'q3': q3
        }
      };

      const { error } = await DB.insert('surveyResponses', record);
      if (!error) {
        alert('¡Gracias por tus respuestas! Fueron registradas de forma totalmente anónima.');
        renderSurveys(container, session);
      } else {
        alert('Error: ' + error.message);
      }
    });
  }
}

/**
 * =============================================================
 * 6. COMUNICADOS INTERNOS
 * =============================================================
 */
async function renderAnnouncements(container, session) {
  await DB.select('announcements');
  const announcements = State.get('announcements') || [];

  container.innerHTML = `
    <div class="page-header">
      <div>
        <h1 class="page-title"><i class="fa-solid fa-bullhorn"></i> Comunicados Internos</h1>
        <p class="page-subtitle">Tablón de avisos corporativos, circulares e información de interés general.</p>
      </div>
      <button class="glass-btn primary" id="btn-new-ann"><i class="fa-solid fa-bullhorn"></i> Publicar Comunicado</button>
    </div>

    <div style="display: grid; grid-template-columns: 1fr; gap: 24px;">
      ${announcements.map(ann => `
        <div class="glass-card" style="border-left: 5px solid ${ann.priority === 'importante' ? 'var(--accent-danger)' : 'var(--accent-primary)'}; border-top: 1px solid var(--border-glass);">
          <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 12px;">
            <h3 style="font-family: var(--font-title); font-weight: 700; font-size: 1.1rem; color: var(--color-text-primary);">${ann.title}</h3>
            <span class="badge-custom ${ann.priority === 'importante' ? 'danger' : 'info'}">${ann.priority}</span>
          </div>
          <p style="font-size: 0.88rem; color: var(--color-text-secondary); line-height: 1.6; margin-bottom: 16px;">
            ${ann.content}
          </p>
          <div style="display: flex; justify-content: space-between; font-size: 0.72rem; color: var(--color-text-muted); border-top: 1px solid var(--border-glass); padding-top: 10px;">
            <span><i class="fa-solid fa-bullhorn"></i> Categoría: ${ann.type}</span>
            <span><i class="fa-solid fa-clock"></i> Publicado: ${new Date(ann.created_at).toLocaleDateString('es-PA')}</span>
          </div>
        </div>
      `).join('')}
    </div>

    <div id="extra-modal-overlay" style="display: none; position: fixed; top:0; left:0; right:0; bottom:0; background:rgba(0,0,0,0.6); backdrop-filter:blur(8px); z-index:1000; align-items:center; justify-content:center; padding:20px;"></div>
  `;

  // EVENTO: Nuevo Anuncio
  container.querySelector('#btn-new-ann').addEventListener('click', () => {
    const overlay = container.querySelector('#extra-modal-overlay');
    overlay.style.display = 'flex';
    overlay.innerHTML = `
      <div class="glass-card" style="max-width: 500px; width: 100%; border-top: 4px solid var(--accent-primary);">
        <h3 style="font-family: var(--font-title); font-weight: 800; font-size: 1.25rem; margin-bottom: 20px;">Publicar Comunicado</h3>
        <form id="form-ann" style="display: flex; flex-direction: column; gap: 16px;">
          <div class="form-group">
            <label>Título del Comunicado *</label>
            <input type="text" id="ann-title" class="form-control" placeholder="e.g. Asueto de Fiestas Patrias" required>
          </div>
          <div class="form-grid" style="grid-template-columns: 1fr 1fr; margin-bottom: 0;">
            <div class="form-group">
              <label>Categoría</label>
              <select id="ann-type" class="form-control">
                <option value="general">General</option>
                <option value="seguridad">Seguridad e Higiene</option>
                <option value="evento">Evento Corporativo</option>
              </select>
            </div>
            <div class="form-group">
              <label>Prioridad</label>
              <select id="ann-prio" class="form-control">
                <option value="normal">Normal</option>
                <option value="importante">Importante (Glow)</option>
              </select>
            </div>
          </div>
          <div class="form-group">
            <label>Cuerpo del Comunicado *</label>
            <textarea id="ann-content" class="form-control" rows="4" required></textarea>
          </div>
          <div style="display: flex; gap: 12px; margin-top: 10px;">
            <button type="submit" class="glass-btn primary" style="flex: 1;">Publicar Anuncio</button>
            <button type="button" class="glass-btn danger" id="btn-close-ann" style="flex: 0.4;">Cancelar</button>
          </div>
        </form>
      </div>
    `;

    overlay.querySelector('#btn-close-ann').addEventListener('click', () => overlay.style.display = 'none');

    overlay.querySelector('#form-ann').addEventListener('submit', async (e) => {
      e.preventDefault();
      const title = overlay.querySelector('#ann-title').value.trim();
      const type = overlay.querySelector('#ann-type').value;
      const prio = overlay.querySelector('#ann-prio').value;
      const content = overlay.querySelector('#ann-content').value.trim();

      const record = {
        title,
        type,
        priority: prio,
        content,
        status: 'activo'
      };

      const { error } = await DB.insert('announcements', record);
      if (!error) {
        alert('Comunicado oficial publicado con éxito.');
        overlay.style.display = 'none';
        renderAnnouncements(container, session);
      } else {
        alert('Error al publicar: ' + error.message);
      }
    });
  });
}
