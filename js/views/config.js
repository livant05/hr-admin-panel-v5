/**
 * =============================================================
 * CONFIG VIEW — HR ADMIN PANEL (PANAMÁ)
 * =============================================================
 * Pantalla de configuración inicial. Gestiona la conexión de la
 * base de datos Supabase, almacenamiento local seguro de tokens y
 * activación/inicio de sesión rápido para el Modo Demo.
 */

import { State } from '../state.js';
import { DB } from '../supabase.js';
import { Router } from '../router.js';

export default {
  async render(container) {
    const isDemo = State.get('demoMode');
    const supabaseUrl = State.get('supabaseUrl') || '';
    const supabaseKey = State.get('supabaseKey') || '';
    const session = State.get('session');

    // Estructura HTML de la vista
    container.innerHTML = `
      <div class="page-header animate-fade-in">
        <div>
          <h1 class="page-title"><i class="fa-solid fa-sliders"></i> Configuración del Sistema</h1>
          <p class="page-subtitle">Gestioná la conexión del panel y los modos de base de datos.</p>
        </div>
      </div>

      <div style="display: grid; grid-template-columns: repeat(auto-fit, minmax(320px, 1fr)); gap: 32px;" class="animate-fade-in">
        
        <!-- CARD: MODO DEMO -->
        <div class="glass-card" style="border-color: ${isDemo ? 'var(--accent-warning)' : 'var(--border-glass)'}; display: flex; flex-direction: column; justify-content: space-between;">
          <div>
            <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 18px;">
              <h2 style="font-family: var(--font-title); font-weight: 700; font-size: 1.25rem;">🚀 Modo Demo Express</h2>
              <span class="badge-custom ${isDemo ? 'warning' : 'info'}">${isDemo ? 'Activo' : 'Disponible'}</span>
            </div>
            <p style="color: var(--color-text-secondary); font-size: 0.88rem; line-height: 1.6; margin-bottom: 20px;">
              Probá todas las funcionalidades del panel de manera instantánea en tu navegador. Los datos se almacenan de forma local y temporal en memoria reactiva.
            </p>
            <div style="background: rgba(245, 158, 11, 0.08); border-left: 4px solid var(--accent-warning); padding: 12px 16px; border-radius: 0 8px 8px 0; margin-bottom: 20px; font-size: 0.82rem;">
              <strong>Usuario Demo:</strong> <code>admin@demo.com</code><br>
              <strong>Contraseña:</strong> cualquiera (4+ caracteres)
            </div>
          </div>
          
          <div>
            ${session && isDemo 
              ? `<div class="badge-custom success" style="width: 100%; justify-content: center; padding: 12px;"><i class="fa-solid fa-circle-check"></i> Ya estás logueado en Modo Demo</div>`
              : `<button class="glass-btn primary" id="btn-login-demo" style="width: 100%; background: linear-gradient(135deg, #f59e0b, #d97706); box-shadow: 0 0 15px rgba(245, 158, 11, 0.25);">
                  <i class="fa-solid fa-bolt"></i> Iniciar Modo Demo
                </button>`
            }
          </div>
        </div>

        <!-- CARD: MODO SUPABASE -->
        <div class="glass-card" style="border-color: ${!isDemo ? 'var(--accent-primary)' : 'var(--border-glass)'};">
          <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 18px;">
            <h2 style="font-family: var(--font-title); font-weight: 700; font-size: 1.25rem;">🗄️ Supabase Cloud (Producción)</h2>
            <span class="badge-custom ${!isDemo ? 'success' : 'info'}">${!isDemo ? 'Conectado' : 'Desactivado'}</span>
          </div>
          <p style="color: var(--color-text-secondary); font-size: 0.88rem; line-height: 1.6; margin-bottom: 20px;">
            Conectá tu propio backend en la nube con Supabase (Auth, PostgreSQL y seguridad RLS multiempresa).
          </p>

          <form id="form-supabase-config" style="display: flex; flex-direction: column; gap: 16px;">
            <div class="form-group">
              <label for="db-url">SUPABASE_URL</label>
              <input type="url" id="db-url" class="form-control" placeholder="https://xxxx.supabase.co" value="${supabaseUrl}" required>
            </div>
            
            <div class="form-group">
              <label for="db-key">SUPABASE_ANON_KEY</label>
              <input type="password" id="db-key" class="form-control" placeholder="eyJhbGciOiJIUzI1NiIsIn..." value="${supabaseKey}" required>
            </div>

            <div style="display: flex; gap: 12px; margin-top: 10px;">
              <button type="submit" class="glass-btn primary" style="flex: 1;"><i class="fa-solid fa-floppy-disk"></i> Guardar y Conectar</button>
              ${!isDemo 
                ? `<button type="button" class="glass-btn danger" id="btn-disconnect-db"><i class="fa-solid fa-power-off"></i> Desconectar</button>`
                : ''
              }
            </div>
          </form>
        </div>

      </div>

      <!-- SECCIÓN INICIAR SESIÓN (SOLO SI SUPABASE ESTÁ CONFIGURADO Y NO LOGUEADO) -->
      ${!isDemo && supabaseUrl && supabaseKey && !session
        ? `
        <div class="glass-card animate-fade-in" style="max-width: 480px; margin: 40px auto 0; border-top: 3px solid var(--accent-primary);">
          <h2 style="font-family: var(--font-title); font-weight: 700; font-size: 1.25rem; margin-bottom: 18px; text-align: center;">Iniciar Sesión Corporativa</h2>
          <form id="form-supabase-login" style="display: flex; flex-direction: column; gap: 16px;">
            <div class="form-group">
              <label for="login-email">Correo Electrónico</label>
              <input type="email" id="login-email" class="form-control" placeholder="nombre@empresa.com" required>
            </div>
            <div class="form-group">
              <label for="login-pass">Contraseña</label>
              <input type="password" id="login-pass" class="form-control" placeholder="••••••••" required>
            </div>
            <button type="submit" class="glass-btn primary" style="width: 100%; margin-top: 8px;"><i class="fa-solid fa-right-to-bracket"></i> Ingresar al Panel</button>
          </form>
        </div>
        `
        : ''
      }
    `;

    this.bindEvents(container);
  },

  bindEvents(container) {
    // EVENTO: Iniciar Modo Demo
    const btnLoginDemo = container.querySelector('#btn-login-demo');
    if (btnLoginDemo) {
      btnLoginDemo.addEventListener('click', async () => {
        try {
          btnLoginDemo.disabled = true;
          btnLoginDemo.innerHTML = `<i class="fa-solid fa-circle-notch fa-spin"></i> Cargando Demo...`;
          
          State.setDemoMode(true);
          const { data, error } = await DB.signIn('admin@demo.com', 'cualquiera');
          
          if (!error) {
            // Actualización del DOM global en header/badge
            document.getElementById('demo-badge').style.display = 'flex';
            document.getElementById('header-user-avatar').textContent = 'AD';
            document.getElementById('header-user-name').textContent = 'Administrador Demo';
            document.getElementById('header-user-role').textContent = 'Super Admin';
            
            // Navega directamente al Dashboard
            Router.navigate('dashboard');
          } else {
            alert(error.message);
            btnLoginDemo.disabled = false;
            btnLoginDemo.innerHTML = `<i class="fa-solid fa-bolt"></i> Iniciar Modo Demo`;
          }
        } catch (e) {
          console.error(e);
          btnLoginDemo.disabled = false;
          btnLoginDemo.innerHTML = `<i class="fa-solid fa-bolt"></i> Iniciar Modo Demo`;
        }
      });
    }

    // EVENTO: Guardar configuración de Supabase
    const formConfig = container.querySelector('#form-supabase-config');
    if (formConfig) {
      formConfig.addEventListener('submit', (e) => {
        e.preventDefault();
        const url = container.querySelector('#db-url').value.trim();
        const key = container.querySelector('#db-key').value.trim();

        if (url && key) {
          State.setDemoMode(false);
          State.setSupabaseConfig(url, key);
          alert('Configuración de base de datos guardada. Iniciando conexión...');
          window.location.reload(); // Recarga para levantar cliente Supabase limpio
        }
      });
    }

    // EVENTO: Desconectar Supabase
    const btnDisconnect = container.querySelector('#btn-disconnect-db');
    if (btnDisconnect) {
      btnDisconnect.addEventListener('click', () => {
        if (confirm('¿Seguro que deseas desconectar Supabase? Se borrarán las credenciales locales.')) {
          State.setSupabaseConfig('', '');
          State.setDemoMode(true);
          DB.signOut();
          window.location.reload();
        }
      });
    }

    // EVENTO: Login Supabase
    const formLogin = container.querySelector('#form-supabase-login');
    if (formLogin) {
      formLogin.addEventListener('submit', async (e) => {
        e.preventDefault();
        const email = container.querySelector('#login-email').value.trim();
        const pass = container.querySelector('#login-pass').value.trim();
        const btnSubmit = formLogin.querySelector('button[type="submit"]');

        btnSubmit.disabled = true;
        btnSubmit.innerHTML = `<i class="fa-solid fa-spinner fa-spin"></i> Conectando...`;

        const { data, error } = await DB.signIn(email, pass);

        if (!error) {
          const session = State.get('session');
          // Actualización de UI
          document.getElementById('demo-badge').style.display = 'none';
          document.getElementById('header-user-avatar').textContent = session.name.substring(0, 2).toUpperCase();
          document.getElementById('header-user-name').textContent = session.name;
          document.getElementById('header-user-role').textContent = session.role;
          
          Router.navigate('dashboard');
        } else {
          alert('Error de autenticación: ' + error.message);
          btnSubmit.disabled = false;
          btnSubmit.innerHTML = `<i class="fa-solid fa-right-to-bracket"></i> Ingresar al Panel`;
        }
      });
    }
  }
};
