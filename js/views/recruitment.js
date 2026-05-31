import { State } from '../state.js';
import { DB } from '../supabase.js';

const stageBadge = {
  applied:   { cls: 'badge-info',      label: 'Postulado' },
  screening: { cls: 'badge-warning',   label: 'Preselección' },
  interview: { cls: 'badge-primary',   label: 'Entrevista' },
  offer:     { cls: 'badge-success',   label: 'Oferta' },
  hired:     { cls: 'badge-dark',      label: 'Contratado' },
  rejected:  { cls: 'badge-secondary', label: 'Descartado' }
};

const statusBadge = {
  open:   { cls: 'badge-success', label: 'Abierta' },
  paused: { cls: 'badge-warning', label: 'Pausada' },
  closed: { cls: 'badge-danger',  label: 'Cerrada' }
};

export default {
  showMiniLoader(container) {
    container.innerHTML = '<div class="text-center p-5"><i class="fas fa-spinner fa-spin fa-2x"></i></div>';
  },

  async render(container) {
    this.showMiniLoader(container);

    await Promise.all([
      DB.select('jobPostings'),
      DB.select('candidates'),
      DB.select('applications')
    ]);

    const jobPostings  = State.get('jobPostings')  || [];
    const candidates   = State.get('candidates')   || [];
    const applications = State.get('applications') || [];

    const totalCandidates = candidates.length;
    const panamenios = candidates.filter(c => c.nationality === 'Panameña').length;
    const pct = totalCandidates > 0 ? Math.round((panamenios / totalCandidates) * 100) : 100;
    const pctBadge = pct >= 90 ? 'badge-success' : 'badge-danger';

    container.innerHTML = `
      <div class="content-header">
        <div class="container-fluid">
          <div class="row mb-2">
            <div class="col-sm-6">
              <h1><i class="fas fa-user-plus mr-2"></i>Reclutamiento y Selección</h1>
            </div>
          </div>
        </div>
      </div>

      <section class="content">
        <div class="container-fluid">

          <!-- TABS -->
          <ul class="nav nav-tabs mb-3" id="rec-tabs">
            <li class="nav-item"><a class="nav-link active" href="#" data-tab="vacantes">Vacantes</a></li>
            <li class="nav-item"><a class="nav-link" href="#" data-tab="candidatos">Candidatos</a></li>
            <li class="nav-item"><a class="nav-link" href="#" data-tab="pipeline">Pipeline</a></li>
          </ul>

          <!-- TAB: VACANTES -->
          <div id="tab-vacantes" class="rec-tab">
            <div class="card">
              <div class="card-header d-flex justify-content-between align-items-center">
                <h3 class="card-title mb-0"><i class="fas fa-briefcase mr-1"></i>Vacantes Activas</h3>
                <button class="btn btn-primary btn-sm" id="btn-nueva-vacante">
                  <i class="fas fa-plus mr-1"></i>Nueva Vacante
                </button>
              </div>
              <div class="card-body p-0">
                <div class="table-responsive">
                  <table class="table table-hover table-sm mb-0">
                    <thead class="thead-light">
                      <tr>
                        <th>Puesto</th>
                        <th>Departamento</th>
                        <th>Ubicación</th>
                        <th>Salario</th>
                        <th>Estado</th>
                        <th>Acciones</th>
                      </tr>
                    </thead>
                    <tbody id="tbody-vacantes">
                      ${jobPostings.length === 0 ? `<tr><td colspan="6" class="text-center text-muted py-3">Sin vacantes registradas.</td></tr>` : jobPostings.map(jp => {
                        const s = statusBadge[jp.status] || statusBadge.open;
                        const salMin = jp.salary_min ? `B/. ${parseFloat(jp.salary_min).toLocaleString('es-PA', {minimumFractionDigits: 2})}` : '—';
                        const salMax = jp.salary_max ? `B/. ${parseFloat(jp.salary_max).toLocaleString('es-PA', {minimumFractionDigits: 2})}` : '—';
                        return `
                          <tr>
                            <td><strong>${jp.title}</strong></td>
                            <td>${jp.department || '—'}</td>
                            <td>${jp.location || '—'}</td>
                            <td style="font-size:.85rem">${salMin} – ${salMax}</td>
                            <td><span class="badge ${s.cls}">${s.label}</span></td>
                            <td>
                              <button class="btn btn-xs btn-outline-secondary mr-1 btn-edit-vacante"
                                data-id="${jp.id}"
                                data-title="${(jp.title||'').replace(/"/g,'&quot;')}"
                                data-department="${(jp.department||'').replace(/"/g,'&quot;')}"
                                data-location="${(jp.location||'').replace(/"/g,'&quot;')}"
                                data-description="${(jp.description||'').replace(/"/g,'&quot;')}"
                                data-requirements="${(jp.requirements||'').replace(/"/g,'&quot;')}"
                                data-salary_min="${jp.salary_min||''}"
                                data-salary_max="${jp.salary_max||''}"
                                data-status="${jp.status||'open'}">
                                <i class="fas fa-edit"></i>
                              </button>
                              <button class="btn btn-xs btn-outline-danger btn-del-vacante" data-id="${jp.id}">
                                <i class="fas fa-trash"></i>
                              </button>
                            </td>
                          </tr>`;
                      }).join('')}
                    </tbody>
                  </table>
                </div>
              </div>
            </div>
          </div>

          <!-- TAB: CANDIDATOS -->
          <div id="tab-candidatos" class="rec-tab" style="display:none">
            <div class="alert alert-info py-2 mb-3" style="font-size:.875rem">
              <i class="fas fa-balance-scale mr-1"></i>
              <strong>Ley 1/1986 — Regla del 90%:</strong> mínimo 90% de empleados deben ser panameños.
              <span class="badge ${pctBadge} ml-2" style="font-size:.8rem">${pct}% panameños (${panamenios}/${totalCandidates})</span>
            </div>
            <div class="card">
              <div class="card-header d-flex justify-content-between align-items-center">
                <h3 class="card-title mb-0"><i class="fas fa-users mr-1"></i>Candidatos</h3>
                <button class="btn btn-primary btn-sm" id="btn-nuevo-candidato">
                  <i class="fas fa-plus mr-1"></i>Nuevo Candidato
                </button>
              </div>
              <div class="card-body p-0">
                <div class="table-responsive">
                  <table class="table table-hover table-sm mb-0">
                    <thead class="thead-light">
                      <tr>
                        <th>Nombre</th>
                        <th>Email</th>
                        <th>Teléfono</th>
                        <th>Nacionalidad</th>
                        <th>Cédula</th>
                        <th>Acciones</th>
                      </tr>
                    </thead>
                    <tbody id="tbody-candidatos">
                      ${candidates.length === 0 ? `<tr><td colspan="6" class="text-center text-muted py-3">Sin candidatos registrados.</td></tr>` : candidates.map(c => `
                        <tr>
                          <td><strong>${c.full_name}</strong></td>
                          <td>${c.email || '—'}</td>
                          <td>${c.phone || '—'}</td>
                          <td>${c.nationality || '—'}</td>
                          <td>${c.id_number || '—'}</td>
                          <td>
                            <button class="btn btn-xs btn-outline-secondary mr-1 btn-edit-candidato"
                              data-id="${c.id}"
                              data-full_name="${(c.full_name||'').replace(/"/g,'&quot;')}"
                              data-email="${(c.email||'').replace(/"/g,'&quot;')}"
                              data-phone="${(c.phone||'').replace(/"/g,'&quot;')}"
                              data-nationality="${(c.nationality||'').replace(/"/g,'&quot;')}"
                              data-id_number="${(c.id_number||'').replace(/"/g,'&quot;')}"
                              data-resume_notes="${(c.resume_notes||'').replace(/"/g,'&quot;')}">
                              <i class="fas fa-edit"></i>
                            </button>
                            <button class="btn btn-xs btn-outline-danger btn-del-candidato" data-id="${c.id}">
                              <i class="fas fa-trash"></i>
                            </button>
                          </td>
                        </tr>`).join('')}
                    </tbody>
                  </table>
                </div>
              </div>
            </div>
          </div>

          <!-- TAB: PIPELINE -->
          <div id="tab-pipeline" class="rec-tab" style="display:none">
            <div class="card">
              <div class="card-header d-flex justify-content-between align-items-center">
                <h3 class="card-title mb-0"><i class="fas fa-stream mr-1"></i>Pipeline de Selección</h3>
                <button class="btn btn-primary btn-sm" id="btn-nueva-postulacion">
                  <i class="fas fa-plus mr-1"></i>Nueva Postulación
                </button>
              </div>
              <div class="card-body p-0">
                <div class="table-responsive">
                  <table class="table table-hover table-sm mb-0">
                    <thead class="thead-light">
                      <tr>
                        <th>Candidato</th>
                        <th>Vacante</th>
                        <th>Etapa</th>
                        <th>Notas</th>
                        <th>Fecha</th>
                        <th>Acciones</th>
                      </tr>
                    </thead>
                    <tbody id="tbody-pipeline">
                      ${applications.length === 0 ? `<tr><td colspan="6" class="text-center text-muted py-3">Sin postulaciones registradas.</td></tr>` : applications.map(a => {
                        const sg = stageBadge[a.stage] || stageBadge.applied;
                        return `
                          <tr>
                            <td><strong>${a.candidate_name || '—'}</strong></td>
                            <td>${a.job_title || '—'}</td>
                            <td><span class="badge ${sg.cls}">${sg.label}</span></td>
                            <td style="max-width:180px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis">${a.notes || '—'}</td>
                            <td>${a.applied_at || '—'}</td>
                            <td>
                              ${a.stage === 'offer' ? `<button class="btn btn-xs btn-success mr-1 btn-contratar" data-id="${a.id}"><i class="fas fa-handshake mr-1"></i>Contratar</button>` : ''}
                              <button class="btn btn-xs btn-outline-secondary mr-1 btn-edit-app"
                                data-id="${a.id}"
                                data-candidate_id="${a.candidate_id||''}"
                                data-job_posting_id="${a.job_posting_id||''}"
                                data-stage="${a.stage||'applied'}"
                                data-notes="${(a.notes||'').replace(/"/g,'&quot;')}"
                                data-applied_at="${a.applied_at||''}">
                                <i class="fas fa-edit"></i>
                              </button>
                              <button class="btn btn-xs btn-outline-danger btn-del-app" data-id="${a.id}">
                                <i class="fas fa-trash"></i>
                              </button>
                            </td>
                          </tr>`;
                      }).join('')}
                    </tbody>
                  </table>
                </div>
              </div>
            </div>
          </div>

        </div>
      </section>

      <!-- MODAL OVERLAY -->
      <div id="recruitment-modal-overlay" style="display:none;position:fixed;inset:0;background:rgba(0,0,0,.55);z-index:1050;align-items:center;justify-content:center">
        <div style="background:var(--card-bg,#fff);color:var(--text,#222);border-radius:10px;width:100%;max-width:560px;max-height:90vh;overflow-y:auto;box-shadow:0 12px 40px rgba(0,0,0,.35)">
          <div style="padding:18px 24px;border-bottom:1px solid var(--border,#dee2e6);display:flex;justify-content:space-between;align-items:center">
            <h5 id="rec-modal-title" style="margin:0;font-weight:700"></h5>
            <button id="rec-modal-close" style="background:none;border:none;font-size:1.4rem;cursor:pointer;color:var(--muted,#6c757d)">&times;</button>
          </div>
          <div style="padding:20px 24px" id="rec-modal-body"></div>
        </div>
      </div>
    `;

    this._bindEvents(container);
  },

  _showModal(title, bodyHtml) {
    const overlay = document.getElementById('recruitment-modal-overlay');
    document.getElementById('rec-modal-title').textContent = title;
    document.getElementById('rec-modal-body').innerHTML = bodyHtml;
    overlay.style.display = 'flex';
  },

  _hideModal() {
    document.getElementById('recruitment-modal-overlay').style.display = 'none';
  },

  _vacantesFormHtml(data = {}) {
    return `
      <form id="form-vacante">
        <input type="hidden" id="vac-id" value="${data.id || ''}">
        <div class="form-group">
          <label>Puesto *</label>
          <input type="text" id="vac-title" class="form-control form-control-sm" value="${data.title || ''}" required>
        </div>
        <div class="form-row">
          <div class="col form-group">
            <label>Departamento</label>
            <input type="text" id="vac-department" class="form-control form-control-sm" value="${data.department || ''}">
          </div>
          <div class="col form-group">
            <label>Ubicación</label>
            <input type="text" id="vac-location" class="form-control form-control-sm" value="${data.location || ''}">
          </div>
        </div>
        <div class="form-group">
          <label>Descripción</label>
          <textarea id="vac-description" class="form-control form-control-sm" rows="3">${data.description || ''}</textarea>
        </div>
        <div class="form-group">
          <label>Requisitos</label>
          <textarea id="vac-requirements" class="form-control form-control-sm" rows="3">${data.requirements || ''}</textarea>
        </div>
        <div class="form-row">
          <div class="col form-group">
            <label>Salario Mín. (B/.)</label>
            <input type="number" id="vac-salary_min" class="form-control form-control-sm" step="0.01" value="${data.salary_min || ''}">
          </div>
          <div class="col form-group">
            <label>Salario Máx. (B/.)</label>
            <input type="number" id="vac-salary_max" class="form-control form-control-sm" step="0.01" value="${data.salary_max || ''}">
          </div>
          <div class="col form-group">
            <label>Estado</label>
            <select id="vac-status" class="form-control form-control-sm">
              <option value="open" ${(data.status||'open') === 'open' ? 'selected' : ''}>Abierta</option>
              <option value="paused" ${data.status === 'paused' ? 'selected' : ''}>Pausada</option>
              <option value="closed" ${data.status === 'closed' ? 'selected' : ''}>Cerrada</option>
            </select>
          </div>
        </div>
        <div class="text-right mt-2">
          <button type="button" class="btn btn-secondary btn-sm mr-2" id="rec-modal-cancel">Cancelar</button>
          <button type="submit" class="btn btn-primary btn-sm"><i class="fas fa-save mr-1"></i>Guardar</button>
        </div>
      </form>`;
  },

  _candidatosFormHtml(data = {}) {
    return `
      <form id="form-candidato">
        <input type="hidden" id="cand-id" value="${data.id || ''}">
        <div class="form-group">
          <label>Nombre completo *</label>
          <input type="text" id="cand-full_name" class="form-control form-control-sm" value="${data.full_name || ''}" required>
        </div>
        <div class="form-row">
          <div class="col form-group">
            <label>Email</label>
            <input type="email" id="cand-email" class="form-control form-control-sm" value="${data.email || ''}">
          </div>
          <div class="col form-group">
            <label>Teléfono</label>
            <input type="tel" id="cand-phone" class="form-control form-control-sm" value="${data.phone || ''}">
          </div>
        </div>
        <div class="form-row">
          <div class="col form-group">
            <label>Nacionalidad</label>
            <input type="text" id="cand-nationality" class="form-control form-control-sm" value="${data.nationality || 'Panameña'}">
          </div>
          <div class="col form-group">
            <label>Cédula / ID</label>
            <input type="text" id="cand-id_number" class="form-control form-control-sm" value="${data.id_number || ''}">
          </div>
        </div>
        <div class="form-group">
          <label>Notas de CV / Perfil</label>
          <textarea id="cand-resume_notes" class="form-control form-control-sm" rows="3">${data.resume_notes || ''}</textarea>
        </div>
        <div class="text-right mt-2">
          <button type="button" class="btn btn-secondary btn-sm mr-2" id="rec-modal-cancel">Cancelar</button>
          <button type="submit" class="btn btn-primary btn-sm"><i class="fas fa-save mr-1"></i>Guardar</button>
        </div>
      </form>`;
  },

  _applicationFormHtml(data = {}) {
    const jobPostings  = State.get('jobPostings')  || [];
    const candidates   = State.get('candidates')   || [];
    const today = new Date().toISOString().split('T')[0];
    return `
      <form id="form-app">
        <input type="hidden" id="app-id" value="${data.id || ''}">
        <div class="form-group">
          <label>Candidato *</label>
          <select id="app-candidate_id" class="form-control form-control-sm" required>
            <option value="">Seleccione...</option>
            ${candidates.map(c => `<option value="${c.id}" ${data.candidate_id === c.id ? 'selected' : ''}>${c.full_name}</option>`).join('')}
          </select>
        </div>
        <div class="form-group">
          <label>Vacante *</label>
          <select id="app-job_posting_id" class="form-control form-control-sm" required>
            <option value="">Seleccione...</option>
            ${jobPostings.map(jp => `<option value="${jp.id}" ${data.job_posting_id === jp.id ? 'selected' : ''}>${jp.title}</option>`).join('')}
          </select>
        </div>
        <div class="form-row">
          <div class="col form-group">
            <label>Etapa</label>
            <select id="app-stage" class="form-control form-control-sm">
              <option value="applied"   ${(data.stage||'applied') === 'applied'   ? 'selected' : ''}>Postulado</option>
              <option value="screening" ${data.stage === 'screening' ? 'selected' : ''}>Preselección</option>
              <option value="interview" ${data.stage === 'interview' ? 'selected' : ''}>Entrevista</option>
              <option value="offer"     ${data.stage === 'offer'     ? 'selected' : ''}>Oferta</option>
              <option value="hired"     ${data.stage === 'hired'     ? 'selected' : ''}>Contratado</option>
              <option value="rejected"  ${data.stage === 'rejected'  ? 'selected' : ''}>Descartado</option>
            </select>
          </div>
          <div class="col form-group">
            <label>Fecha postulación</label>
            <input type="date" id="app-applied_at" class="form-control form-control-sm" value="${data.applied_at || today}">
          </div>
        </div>
        <div class="form-group">
          <label>Notas</label>
          <textarea id="app-notes" class="form-control form-control-sm" rows="3">${data.notes || ''}</textarea>
        </div>
        <div class="text-right mt-2">
          <button type="button" class="btn btn-secondary btn-sm mr-2" id="rec-modal-cancel">Cancelar</button>
          <button type="submit" class="btn btn-primary btn-sm"><i class="fas fa-save mr-1"></i>Guardar</button>
        </div>
      </form>`;
  },

  _bindEvents(container) {
    const self = this;

    // Tab switching
    container.querySelectorAll('[data-tab]').forEach(link => {
      link.addEventListener('click', e => {
        e.preventDefault();
        const tab = link.dataset.tab;
        container.querySelectorAll('[data-tab]').forEach(l => l.classList.remove('active'));
        link.classList.add('active');
        container.querySelectorAll('.rec-tab').forEach(t => t.style.display = 'none');
        const el = document.getElementById(`tab-${tab}`);
        if (el) el.style.display = '';
      });
    });

    // Modal close
    document.getElementById('rec-modal-close').addEventListener('click', () => self._hideModal());
    document.getElementById('recruitment-modal-overlay').addEventListener('click', e => {
      if (e.target === e.currentTarget) self._hideModal();
    });
    container.addEventListener('click', e => {
      if (e.target.id === 'rec-modal-cancel' || e.target.closest('#rec-modal-cancel')) self._hideModal();
    });

    // ── VACANTES ──────────────────────────────────────────

    container.querySelector('#btn-nueva-vacante').addEventListener('click', () => {
      self._showModal('Nueva Vacante', self._vacantesFormHtml());
      document.getElementById('form-vacante').addEventListener('submit', async e => {
        e.preventDefault();
        await self._saveVacante(container);
      });
    });

    container.querySelectorAll('.btn-edit-vacante').forEach(btn => {
      btn.addEventListener('click', () => {
        const d = btn.dataset;
        self._showModal('Editar Vacante', self._vacantesFormHtml({
          id: d.id, title: d.title, department: d.department, location: d.location,
          description: d.description, requirements: d.requirements,
          salary_min: d.salary_min, salary_max: d.salary_max, status: d.status
        }));
        document.getElementById('form-vacante').addEventListener('submit', async e => {
          e.preventDefault();
          await self._saveVacante(container);
        });
      });
    });

    container.querySelectorAll('.btn-del-vacante').forEach(btn => {
      btn.addEventListener('click', async () => {
        if (!confirm('¿Eliminar esta vacante?')) return;
        await DB.delete('jobPostings', btn.dataset.id);
        self.render(container);
      });
    });

    // ── CANDIDATOS ────────────────────────────────────────

    container.querySelector('#btn-nuevo-candidato').addEventListener('click', () => {
      self._showModal('Nuevo Candidato', self._candidatosFormHtml());
      document.getElementById('form-candidato').addEventListener('submit', async e => {
        e.preventDefault();
        await self._saveCandidato(container);
      });
    });

    container.querySelectorAll('.btn-edit-candidato').forEach(btn => {
      btn.addEventListener('click', () => {
        const d = btn.dataset;
        self._showModal('Editar Candidato', self._candidatosFormHtml({
          id: d.id, full_name: d.full_name, email: d.email, phone: d.phone,
          nationality: d.nationality, id_number: d.id_number, resume_notes: d.resume_notes
        }));
        document.getElementById('form-candidato').addEventListener('submit', async e => {
          e.preventDefault();
          await self._saveCandidato(container);
        });
      });
    });

    container.querySelectorAll('.btn-del-candidato').forEach(btn => {
      btn.addEventListener('click', async () => {
        if (!confirm('¿Eliminar este candidato?')) return;
        await DB.delete('candidates', btn.dataset.id);
        self.render(container);
      });
    });

    // ── PIPELINE ──────────────────────────────────────────

    container.querySelector('#btn-nueva-postulacion').addEventListener('click', () => {
      self._showModal('Nueva Postulación', self._applicationFormHtml());
      document.getElementById('form-app').addEventListener('submit', async e => {
        e.preventDefault();
        await self._saveApplication(container);
      });
    });

    container.querySelectorAll('.btn-edit-app').forEach(btn => {
      btn.addEventListener('click', () => {
        const d = btn.dataset;
        self._showModal('Editar Postulación', self._applicationFormHtml({
          id: d.id, candidate_id: d.candidate_id, job_posting_id: d.job_posting_id,
          stage: d.stage, notes: d.notes, applied_at: d.applied_at
        }));
        document.getElementById('form-app').addEventListener('submit', async e => {
          e.preventDefault();
          await self._saveApplication(container);
        });
      });
    });

    container.querySelectorAll('.btn-del-app').forEach(btn => {
      btn.addEventListener('click', async () => {
        if (!confirm('¿Eliminar esta postulación?')) return;
        await DB.delete('applications', btn.dataset.id);
        self.render(container);
      });
    });

    container.querySelectorAll('.btn-contratar').forEach(btn => {
      btn.addEventListener('click', async () => {
        if (!confirm('¿Marcar como Contratado?')) return;
        await DB.update('applications', btn.dataset.id, { stage: 'hired' });
        self.render(container);
      });
    });
  },

  async _saveVacante(container) {
    const id          = document.getElementById('vac-id').value;
    const title       = document.getElementById('vac-title').value.trim();
    const department  = document.getElementById('vac-department').value.trim();
    const location    = document.getElementById('vac-location').value.trim();
    const description = document.getElementById('vac-description').value.trim();
    const requirements= document.getElementById('vac-requirements').value.trim();
    const salary_min  = parseFloat(document.getElementById('vac-salary_min').value) || null;
    const salary_max  = parseFloat(document.getElementById('vac-salary_max').value) || null;
    const status      = document.getElementById('vac-status').value;

    if (!title) return;

    const payload = { title, department, location, description, requirements, salary_min, salary_max, status };

    if (id) {
      await DB.update('jobPostings', id, payload);
    } else {
      await DB.insert('jobPostings', payload);
    }
    this._hideModal();
    this.render(container);
  },

  async _saveCandidato(container) {
    const id           = document.getElementById('cand-id').value;
    const full_name    = document.getElementById('cand-full_name').value.trim();
    const email        = document.getElementById('cand-email').value.trim();
    const phone        = document.getElementById('cand-phone').value.trim();
    const nationality  = document.getElementById('cand-nationality').value.trim();
    const id_number    = document.getElementById('cand-id_number').value.trim();
    const resume_notes = document.getElementById('cand-resume_notes').value.trim();

    if (!full_name) return;

    const payload = { full_name, email, phone, nationality, id_number, resume_notes };

    if (id) {
      await DB.update('candidates', id, payload);
    } else {
      await DB.insert('candidates', payload);
    }
    this._hideModal();
    this.render(container);
  },

  async _saveApplication(container) {
    const id             = document.getElementById('app-id').value;
    const candidate_id   = document.getElementById('app-candidate_id').value;
    const job_posting_id = document.getElementById('app-job_posting_id').value;
    const stage          = document.getElementById('app-stage').value;
    const notes          = document.getElementById('app-notes').value.trim();
    const applied_at     = document.getElementById('app-applied_at').value;

    if (!candidate_id || !job_posting_id) return;

    const candidates  = State.get('candidates')  || [];
    const jobPostings = State.get('jobPostings') || [];
    const candidate   = candidates.find(c => c.id === candidate_id);
    const job         = jobPostings.find(j => j.id === job_posting_id);

    const payload = {
      candidate_id,
      candidate_name: candidate ? candidate.full_name : '',
      job_posting_id,
      job_title: job ? job.title : '',
      stage,
      notes,
      applied_at
    };

    if (id) {
      await DB.update('applications', id, payload);
    } else {
      await DB.insert('applications', payload);
    }
    this._hideModal();
    this.render(container);
  }
};
