/**
 * =============================================================
 * APP ROUTER — HR ADMIN PANEL (PANAMÁ)
 * =============================================================
 * Manejador de enrutamiento del lado del cliente (SPA) basado
 * en hashes de la URL. Controla qué vista se inyecta en el DOM
 * principal y gestiona la activación visual del sidebar.
 */

import { State } from './state.js';

// Mapeo dinámico de rutas de vistas
const ROUTES = {
  'dashboard': () => import('./views/dashboard.js'),
  'employees': () => import('./views/employees.js'),
  'attendance': () => import('./views/attendance.js'),
  'vacations': () => import('./views/vacations.js'),
  'payroll': () => import('./views/payroll.js'),
  'config': () => import('./views/config.js'),
  
  'recruitment': () => import('./views/recruitment.js'),

  // Módulos extra consolidados en extraModules.js
  'medical-records': () => import('./views/extraModules.js'),
  'evaluations': () => import('./views/extraModules.js'),
  'uniforms': () => import('./views/extraModules.js'),
  'trainings': () => import('./views/extraModules.js'),
  'surveys': () => import('./views/extraModules.js'),
  'announcements': () => import('./views/extraModules.js')
};

class AppRouter {
  constructor() {
    this.contentArea = document.getElementById('app-content');
    
    // Escucha de cambio de hash
    window.addEventListener('hashchange', () => this.handleRouting());
  }

  /**
   * Inicializa el ruteador y ejecuta la primera verificación de ruta
   */
  init() {
    this.handleRouting();
  }

  /**
   * Navega programáticamente a un hash
   * @param {string} route Nombre de la ruta (e.g. 'dashboard')
   */
  navigate(route) {
    window.location.hash = `#/${route}`;
  }

  /**
   * Resuelve la ruta actual basada en el hash de la URL
   */
  async handleRouting() {
    const hash = window.location.hash || '#/dashboard';
    let path = hash.substring(2); // Remueve '#/'
    
    // Si la ruta no está registrada, fuerza redirección al Dashboard
    if (!ROUTES[path]) {
      path = 'dashboard';
      this.navigate(path);
      return;
    }

    // Guardia de seguridad: Si no hay sesión iniciada y la ruta no es 'config', redireccionar
    const session = State.get('session');
    const isDemo = State.get('demoMode');
    
    if (!session && path !== 'config') {
      // Si no hay configuración de Supabase y tampoco está en Modo Demo, forzar pantalla de configuración
      if (!isDemo && (!State.get('supabaseUrl') || !State.get('supabaseKey'))) {
        this.navigate('config');
        return;
      }
    }

    // Actualiza el estado global de la vista activa
    State.set('activeView', path);

    // Actualización visual del Sidebar (links activos)
    this.updateSidebarActiveLink(path);

    // Carga perezosa (lazy-loading) e inyección de la vista
    try {
      this.showLoader();
      
      const moduleLoader = ROUTES[path];
      const viewModule = await moduleLoader();
      
      // Limpia el contenedor y renderiza la vista
      this.contentArea.innerHTML = '';
      
      // Creamos un wrapper con clase de animación fade-in
      const wrapper = document.createElement('div');
      wrapper.className = 'animate-fade-in';
      
      // Cada módulo de vista exportará una clase/objeto con un método render()
      // En el caso de extraModules.js, exportará métodos específicos o resolverá dinámicamente según la ruta
      if (path === 'medical-records' || path === 'evaluations' || path === 'uniforms' || 
          path === 'trainings' || path === 'surveys' || path === 'announcements') {
        // Ejecuta la renderización del módulo extra dinámico
        await viewModule.renderExtraView(path, wrapper);
      } else {
        // Vistas core estándar
        await viewModule.default.render(wrapper);
      }
      
      this.contentArea.appendChild(wrapper);
      
    } catch (e) {
      console.error(`Error cargando la vista ${path}:`, e);
      this.contentArea.innerHTML = `
        <div class="glass-card text-center animate-fade-in" style="padding: 40px;">
          <i class="fa-solid fa-triangle-exclamation text-danger" style="font-size: 3rem; margin-bottom: 20px;"></i>
          <h2 class="text-danger" style="font-family: var(--font-title); font-weight: 700; margin-bottom: 12px;">Error de Carga</h2>
          <p style="color: var(--color-text-secondary); margin-bottom: 24px;">Hubo un problema al cargar el módulo solicitado de forma dinámica.</p>
          <code>Detalle: ${e.message}</code>
          <div style="margin-top: 24px;">
            <button class="glass-btn primary" onclick="window.location.reload()"><i class="fa-solid fa-rotate"></i> Reintentar Cargar</button>
          </div>
        </div>
      `;
    }
  }

  /**
   * Activa visualmente el enlace en el menú lateral
   */
  updateSidebarActiveLink(path) {
    const links = document.querySelectorAll('.sidebar-nav .nav-link');
    links.forEach(link => {
      if (link.getAttribute('data-link') === path) {
        link.classList.add('active');
      } else {
        link.classList.remove('active');
      }
    });
  }

  /**
   * Muestra un indicador de carga elegante
   */
  showLoader() {
    this.contentArea.innerHTML = `
      <div style="display: flex; flex-direction: column; align-items: center; justify-content: center; min-height: 40vh;" class="animate-fade-in">
        <i class="fa-solid fa-spinner fa-spin" style="font-size: 2.5rem; color: var(--accent-primary); margin-bottom: 16px;"></i>
        <span style="font-size: 0.88rem; color: var(--color-text-secondary); font-weight: 500; letter-spacing: 0.5px;">Cargando módulo de Recursos Humanos...</span>
      </div>
    `;
  }
}

export const Router = new AppRouter();
