/**
 * =============================================================
 * STATE MANAGER — HR ADMIN PANEL (PANAMÁ)
 * =============================================================
 * Manejador de estado reactivo centralizado. Utiliza un patrón
 * de publicación-suscripción simple para notificar cambios en el
 * estado global de la aplicación (SPA).
 */

class StateManager {
  constructor() {
    // Configuración inicial de persistencia en LocalStorage
    this.config = {
      supabaseUrl: localStorage.getItem('hr_supabase_url') || '',
      supabaseKey: localStorage.getItem('hr_supabase_key') || '',
      demoMode: localStorage.getItem('hr_demo_mode') !== 'false' // Por defecto true si no está seteado en false
    };

    // Estado global de la aplicación
    this.state = {
      session: null,            // Datos del usuario logueado: { email, role, name, id }
      companyId: null,          // ID de la empresa activa
      companies: [],            // Listado de empresas contratadas
      activeView: 'dashboard',  // Sección actual en renderización
      theme: localStorage.getItem('hr_theme') || 'dark', // Tema activo: dark o light
      
      // Caché local en memoria de todas las colecciones de la base de datos
      employees: [],
      attendance: [],
      leaveBalances: [],
      leaveRequests: [],
      overtime: [],
      deductions: [],
      payrollHistory: [],
      liquidationHistory: [],
      medicalRecords: [],
      evaluations: [],
      uniforms: [],
      trainings: [],
      enrollments: [],
      surveys: [],
      surveyResponses: [],
      announcements: [],
      jobPostings: [],
      candidates: [],
      applications: []
    };

    // Registro de listeners reactivos para suscripciones
    this.listeners = {};
  }

  /**
   * Guarda credenciales de Supabase en LocalStorage y actualiza el config
   */
  setSupabaseConfig(url, key) {
    this.config.supabaseUrl = url;
    this.config.supabaseKey = key;
    localStorage.setItem('hr_supabase_url', url);
    localStorage.setItem('hr_supabase_key', key);
    this.trigger('config', this.config);
  }

  /**
   * Alterna entre Modo Demo local o Modo Producción con Supabase
   */
  setDemoMode(isDemo) {
    this.config.demoMode = isDemo;
    localStorage.setItem('hr_demo_mode', isDemo ? 'true' : 'false');
    this.trigger('config', this.config);
  }

  /**
   * Cambia el tema (oscuro/claro) del panel
   */
  setTheme(theme) {
    this.state.theme = theme;
    localStorage.setItem('hr_theme', theme);
    document.documentElement.setAttribute('data-theme', theme);
    this.trigger('theme', theme);
  }

  /**
   * Retorna una propiedad de configuración o estado
   */
  get(key) {
    if (key in this.config) return this.config[key];
    return this.state[key];
  }

  /**
   * Actualiza una porción del estado y notifica a los suscriptores
   */
  set(key, value) {
    this.state[key] = value;
    this.trigger(key, value);
  }

  /**
   * Se suscribe a los cambios de un nodo específico del estado
   * @param {string} key Nombre del nodo (e.g. 'session', 'employees')
   * @param {function} callback Función a ejecutar cuando cambie el nodo
   */
  subscribe(key, callback) {
    if (!this.listeners[key]) {
      this.listeners[key] = [];
    }
    this.listeners[key].push(callback);
    
    // Retorna una función para cancelar la suscripción
    return () => {
      this.listeners[key] = this.listeners[key].filter(cb => cb !== callback);
    };
  }

  /**
   * Dispara todos los callbacks suscritos a un nodo del estado
   */
  trigger(key, value) {
    if (this.listeners[key]) {
      this.listeners[key].forEach(callback => {
        try {
          callback(value);
        } catch (e) {
          console.error(`Error en listener de ${key}:`, e);
        }
      });
    }
  }

  /**
   * Resetea toda la caché en memoria cuando se cierra la sesión
   */
  clearCache() {
    this.state.session = null;
    this.state.companyId = null;
    this.state.companies = [];
    
    const collections = [
      'employees', 'attendance', 'leaveBalances', 'leaveRequests',
      'overtime', 'deductions', 'payrollHistory', 'liquidationHistory',
      'medicalRecords', 'evaluations', 'uniforms', 'trainings',
      'enrollments', 'surveys', 'surveyResponses', 'announcements',
      'jobPostings', 'candidates', 'applications'
    ];
    
    collections.forEach(col => {
      this.state[col] = [];
      this.trigger(col, []);
    });
    
    this.trigger('session', null);
  }
}

// Exportamos una instancia única del manejador de estado (Singleton)
export const State = new StateManager();
