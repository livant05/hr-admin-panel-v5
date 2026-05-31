/**
 * =============================================================
 * BOOTSTRAP MODULE (ENTRY POINT) — HR ADMIN PANEL (PANAMÁ)
 * =============================================================
 * Inicializador principal de la aplicación SPA. Registra
 * escuchas globales de interacción de la UI, sincroniza el tema,
 * valida el inicio de sesión reactivo y arranca el Router.
 */

import { State } from './state.js';
import { Router } from './router.js';
import { DB } from './supabase.js';

document.addEventListener('DOMContentLoaded', () => {
  console.log('🏁 Iniciando HR Admin Panel v5.0...');

  // 1. Vincular componentes globales de UI
  initGlobalUI();

  // 2. Reactividad de sesión: Sincronizar cabecera cuando el usuario se loguea/desloguea
  State.subscribe('session', (session) => {
    const avatarEl = document.getElementById('header-user-avatar');
    const nameEl = document.getElementById('header-user-name');
    const roleEl = document.getElementById('header-user-role');
    const coSelectorWrap = document.getElementById('header-company-wrap');

    if (session) {
      // Usuario activo
      avatarEl.textContent = session.name.substring(0, 2).toUpperCase();
      nameEl.textContent = session.name;
      roleEl.textContent = session.role;
      coSelectorWrap.style.display = 'block';
      
      // Sincronizar selector de empresa
      const coSelect = document.getElementById('company-selector');
      const companies = State.get('companies') || [];
      coSelect.innerHTML = companies.map(c => `
        <option value="${c.id}" ${c.id === session.company_id ? 'selected' : ''}>${c.name}</option>
      `).join('');

    } else {
      // Sesión inactiva
      avatarEl.textContent = 'AD';
      nameEl.textContent = 'Administrador';
      roleEl.textContent = 'Super Admin';
      coSelectorWrap.style.display = 'none';
    }
  });

  // 3. Reactividad del Modo Demo: Mostrar/Ocultar badge flotante
  State.subscribe('config', (config) => {
    const badge = document.getElementById('demo-badge');
    if (config.demoMode) {
      badge.style.display = 'flex';
    } else {
      badge.style.display = 'none';
    }
  });

  // 4. Inicializar y lanzar el Ruteador
  Router.init();
});

/**
 * Registra manejadores de eventos para controles del Layout Global
 */
function initGlobalUI() {
  const body = document.body;

  // COLLAPSE SIDEBAR (Menú hamburguesa en móviles / contraer en desktop)
  const btnToggle = document.getElementById('btn-menu-toggle');
  if (btnToggle) {
    btnToggle.addEventListener('click', () => {
      body.classList.toggle('sidebar-open');
      
      // Rotar ícono de barras
      const icon = btnToggle.querySelector('i');
      if (icon.classList.contains('fa-bars')) {
        icon.className = 'fa-solid fa-arrow-right-to-bracket';
      } else {
        icon.className = 'fa-solid fa-bars';
      }
    });
  }

  // THEME TOGGLE (Modo Oscuro / Claro)
  const btnTheme = document.getElementById('btn-theme-toggle');
  const activeTheme = State.get('theme');
  
  // Sincronizar DOM con tema inicial del State
  document.documentElement.setAttribute('data-theme', activeTheme);
  updateThemeIcon(btnTheme, activeTheme);

  if (btnTheme) {
    btnTheme.addEventListener('click', () => {
      const currentTheme = State.get('theme');
      const nextTheme = currentTheme === 'dark' ? 'light' : 'dark';
      
      State.setTheme(nextTheme);
      updateThemeIcon(btnTheme, nextTheme);
    });
  }

  // USER PROFILE CLICK -> CERRAR SESIÓN
  const profileWidget = document.querySelector('.user-profile');
  if (profileWidget) {
    profileWidget.addEventListener('click', () => {
      const session = State.get('session');
      if (session) {
        if (confirm(`¿Deseás cerrar la sesión de ${session.name}? Se limpiará la memoria de trabajo.`)) {
          DB.signOut();
          Router.navigate('config');
        }
      } else {
        Router.navigate('config');
      }
    });
  }
}

/**
 * Actualiza el ícono del botón de tema (sol / luna)
 */
function updateThemeIcon(button, theme) {
  if (!button) return;
  const icon = button.querySelector('i');
  if (theme === 'light') {
    icon.className = 'fa-solid fa-sun';
    button.title = 'Alternar a Modo Oscuro';
  } else {
    icon.className = 'fa-solid fa-moon';
    button.title = 'Alternar a Modo Claro';
  }
}
