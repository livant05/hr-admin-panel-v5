/**
 * =============================================================
 * DATABASE CONNECTOR (SUPABASE / DEMO) — HR PANEL (PANAMÁ)
 * =============================================================
 * Adaptador de persistencia agnóstico. Redirige dinámicamente las
 * consultas hacia Supabase (con autenticación JWT y multitenancy RLS)
 * o hacia la caché en memoria del State (Modo Demo) sin alterar
 * la lógica de negocio de las vistas.
 */

import { State } from './state.js';

// Cargamos dinámicamente el cliente de Supabase usando ESM CDN
let supabase = null;

/**
 * Inicializa el cliente oficial de Supabase con los parámetros configurados
 */
function initSupabase() {
  const url = State.get('supabaseUrl');
  const key = State.get('supabaseKey');
  const isDemo = State.get('demoMode');

  if (!isDemo && url && key) {
    try {
      // Importamos la SDK oficial compilada como ES Module directamente
      import('https://cdn.jsdelivr.net/npm/@supabase/supabase-js@2/+esm')
        .then(module => {
          supabase = module.createClient(url, key);
          console.log('✅ Conexión con Supabase establecida exitosamente.');
        })
        .catch(err => {
          console.error('❌ Error importando la SDK de Supabase por CDN:', err);
        });
    } catch (e) {
      console.error('❌ Error inicializando Supabase:', e);
    }
  } else {
    supabase = null;
  }
}

// Escuchamos cambios en la configuración para reinicializar el cliente
State.subscribe('config', () => {
  initSupabase();
});

// Inicialización automática
initSupabase();

/**
 * =============================================================
 * SERVICIO DB UNIFICADO
 * =============================================================
 */
export const DB = {
  /**
   * Autenticación de usuarios (Login)
   */
  async signIn(email, password) {
    const isDemo = State.get('demoMode');
    
    if (isDemo) {
      // Login Simulador Demo
      if (email.toLowerCase() === 'admin@demo.com') {
        const dummySession = {
          id: 'demo-admin-uuid',
          email: 'admin@demo.com',
          name: 'Administrador Demo',
          role: 'Admin',
          company_id: 'demo-company-uuid'
        };
        State.set('session', dummySession);
        State.set('companyId', 'demo-company-uuid');
        State.set('companies', [{ id: 'demo-company-uuid', name: 'Empresa Demo S.A.', ruc: '8-888-888 DV 88' }]);
        
        // Dispara la carga de datos simulados en memoria
        const { initDemoData } = await import('./utils/demoData.js');
        initDemoData();
        
        return { data: { user: dummySession }, error: null };
      }
      return { data: null, error: { message: 'Credenciales inválidas. Usa admin@demo.com y cualquier clave.' } };
    }

    // Producción con Supabase
    if (!supabase) return { data: null, error: { message: 'El cliente de Supabase no está configurado.' } };
    
    const { data, error } = await supabase.auth.signInWithPassword({ email, password });
    if (error) return { data: null, error };

    // Extraemos la sesión y metadatos
    const user = data.user;
    const companyId = user.user_metadata?.company_id || null;
    
    const sessionData = {
      id: user.id,
      email: user.email,
      name: user.user_metadata?.name || 'Usuario',
      role: user.user_metadata?.role || 'Empleado',
      company_id: companyId
    };

    State.set('session', sessionData);
    State.set('companyId', companyId);
    
    // Carga de la info de empresa activa
    if (companyId) {
      const { data: coData } = await supabase.from('companies').select('*').eq('id', companyId).maybeSingle();
      if (coData) {
        State.set('companies', [coData]);
      }
    }

    return { data, error: null };
  },

  /**
   * Registro de nuevas empresas y cuentas administrativas
   */
  async signUp(email, password, name, companyName) {
    const isDemo = State.get('demoMode');
    
    if (isDemo) {
      return { data: null, error: { message: 'El registro de usuarios no está disponible en Modo Demo.' } };
    }

    if (!supabase) return { data: null, error: { message: 'Supabase no configurado.' } };

    // 1. Crear empresa primero
    // NOTA: Para evitar el problema de gallina-huevo de RLS, la inserción directa en companies
    // requiere que no tenga RLS activo o que el registro se haga vía Supabase Auth trigger.
    // Usamos una llamada directa de inserción de empresa.
    const { data: company, error: coErr } = await supabase
      .from('companies')
      .insert({ name: companyName })
      .select()
      .single();

    if (coErr) return { data: null, error: coErr };

    // 2. Registrar usuario en Auth con metadatos de la empresa creada
    const { data, error } = await supabase.auth.signUp({
      email,
      password,
      options: {
        data: {
          name: name,
          role: 'Admin',
          company_id: company.id
        }
      }
    });

    return { data, error };
  },

  /**
   * Cierre de sesión y limpieza de la aplicación
   */
  async signOut() {
    const isDemo = State.get('demoMode');
    if (!isDemo && supabase) {
      await supabase.auth.signOut();
    }
    State.clearCache();
  },

  /**
   * Obtiene todos los registros de una tabla (soporta Modo Demo y Supabase)
   */
  async select(table) {
    const isDemo = State.get('demoMode');
    
    if (isDemo) {
      // Retorna directamente los datos locales almacenados en el State
      return { data: State.get(table) || [], error: null };
    }

    if (!supabase) return { data: [], error: { message: 'Supabase no inicializado.' } };

    const { data, error } = await supabase
      .from(this.resolveDbTableName(table))
      .select('*')
      .order('created_at', { ascending: false });

    if (!error && data) {
      State.set(table, data); // Actualiza la caché del estado reactivamente
    }
    
    return { data, error };
  },

  /**
   * Inserta un nuevo registro
   */
  async insert(table, record) {
    const isDemo = State.get('demoMode');
    
    if (isDemo) {
      const collection = State.get(table) || [];
      const newRecord = {
        id: crypto.randomUUID(),
        company_id: State.get('companyId'),
        created_at: new Date().toISOString(),
        ...record
      };
      
      collection.unshift(newRecord);
      State.set(table, collection);
      return { data: newRecord, error: null };
    }

    if (!supabase) return { data: null, error: { message: 'Supabase no inicializado.' } };

    const dbTable = this.resolveDbTableName(table);
    const enrichedRecord = {
      company_id: State.get('companyId'),
      ...record
    };

    const { data, error } = await supabase
      .from(dbTable)
      .insert(enrichedRecord)
      .select()
      .single();

    if (!error && data) {
      // Insertar en caché local
      const collection = State.get(table) || [];
      collection.unshift(data);
      State.set(table, collection);
    }

    return { data, error };
  },

  /**
   * Actualiza un registro existente
   */
  async update(table, id, changes) {
    const isDemo = State.get('demoMode');

    if (isDemo) {
      const collection = State.get(table) || [];
      const index = collection.findIndex(item => item.id === id);
      
      if (index !== -1) {
        collection[index] = {
          ...collection[index],
          ...changes,
          updated_at: new Date().toISOString()
        };
        State.set(table, [...collection]);
        return { data: collection[index], error: null };
      }
      return { data: null, error: { message: 'Registro no encontrado en memoria.' } };
    }

    if (!supabase) return { data: null, error: { message: 'Supabase no inicializado.' } };

    const dbTable = this.resolveDbTableName(table);
    const { data, error } = await supabase
      .from(dbTable)
      .update(changes)
      .eq('id', id)
      .select()
      .single();

    if (!error && data) {
      // Actualiza la caché local
      const collection = State.get(table) || [];
      const index = collection.findIndex(item => item.id === id);
      if (index !== -1) {
        collection[index] = data;
        State.set(table, [...collection]);
      }
    }

    return { data, error };
  },

  /**
   * Elimina un registro de la base de datos
   */
  async delete(table, id) {
    const isDemo = State.get('demoMode');

    if (isDemo) {
      const collection = State.get(table) || [];
      const filtered = collection.filter(item => item.id !== id);
      State.set(table, filtered);
      return { data: true, error: null };
    }

    if (!supabase) return { data: false, error: { message: 'Supabase no inicializado.' } };

    const dbTable = this.resolveDbTableName(table);
    const { error } = await supabase
      .from(dbTable)
      .delete()
      .eq('id', id);

    if (!error) {
      // Actualiza la caché local
      const collection = State.get(table) || [];
      const filtered = collection.filter(item => item.id !== id);
      State.set(table, filtered);
      return { data: true, error: null };
    }

    return { data: false, error };
  },

  /**
   * Mapeador helper para traducir claves del State de JS a los nombres exactos 
   * de las tablas del schema PostgreSQL.
   */
  resolveDbTableName(stateKey) {
    const mapping = {
      'employees': 'employees',
      'attendance': 'attendance_logs',
      'leaveBalances': 'leave_balances',
      'leaveRequests': 'leave_requests',
      'overtime': 'overtime_logs',
      'deductions': 'deductions',
      'payrollHistory': 'payroll_history',
      'liquidationHistory': 'liquidation_history',
      'medicalRecords': 'medical_records',
      'evaluations': 'evaluations',
      'uniforms': 'uniforms',
      'trainings': 'trainings',
      'enrollments': 'enrollments',
      'surveys': 'surveys',
      'surveyResponses': 'survey_responses',
      'announcements': 'announcements',
      'jobPostings': 'job_postings',
      'candidates': 'candidates',
      'applications': 'applications'
    };
    return mapping[stateKey] || stateKey;
  }
};
