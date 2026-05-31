/**
 * =============================================================
 * DATA SIMULATOR (MOCK DATA EXPANDED) — HR PANEL (PANAMÁ)
 * =============================================================
 * Inyecta un volumen masivo y realista de datos (entre 10 y 25
 * registros por módulo) para que el Modo Demo se muestre con la 
 * densidad de información de una corporación en producción.
 */

import { State } from '../state.js';

export function initDemoData() {
  console.log('⚡ Generando volumen masivo de datos Demo (10-25 registros por sección)...');

  const companyId = 'demo-company-uuid';

  // 1. EMPRESAS
  const companies = [
    {
      id: companyId,
      name: 'Corporación Transístmica S.A.',
      ruc: '1552364-1-654321 DV 89',
      address: 'Ciudad de Panamá, Costa del Este, Torre Financial, Piso 18',
      phone: '+507 300-4500',
      created_at: new Date().toISOString()
    }
  ];

  // 2. DEPARTAMENTOS
  const departments = [
    { id: 'd1', company_id: companyId, name: 'Administración' },
    { id: 'd2', company_id: companyId, name: 'Recursos Humanos' },
    { id: 'd3', company_id: companyId, name: 'Contabilidad & Finanzas' },
    { id: 'd4', company_id: companyId, name: 'Tecnología' },
    { id: 'd5', company_id: companyId, name: 'Operaciones & Logística' }
  ];

  // 3. CARGOS
  const positions = [
    { id: 'p1', company_id: companyId, name: 'Gerente General' },
    { id: 'p2', company_id: companyId, name: 'Director de RRHH' },
    { id: 'p3', company_id: companyId, name: 'Contador General' },
    { id: 'p4', company_id: companyId, name: 'Desarrollador Full Stack' },
    { id: 'p5', company_id: companyId, name: 'Supervisor de Logística' },
    { id: 'p6', company_id: companyId, name: 'Analista de Planilla' },
    { id: 'p7', company_id: companyId, name: 'Asistente Administrativo' },
    { id: 'p8', company_id: companyId, name: 'Asesor Legal Interno' },
    { id: 'p9', company_id: companyId, name: 'Desarrollador Front End' },
    { id: 'p10', company_id: companyId, name: 'Encargado de Compras' }
  ];

  // 4. COLABORADORES (12 Colaboradores Panameños)
  const employees = [
    { id: 'emp1', company_id: companyId, first_name: 'Anel', last_name: 'Rodríguez', cedula: '8-812-345', ss_number: '12-34-5678', dv: '92', birth_date: '1985-05-12', age: 41, sex: 'M', marital_status: 'casado', blood_type: 'O+', nationality: 'Panameño', address: 'Panamá Oeste, Costa Verde', phone: '+507 6612-3456', email: 'anel.rodriguez@demo.com', position: 'Gerente General', department: 'Administración', branch: 'Sede Principal', salary: 4500.00, weekly_hours: 40, monthly_hours: 173.3, hourly_rate: 25.9654, contract_type: 'indefinido', start_date: '2015-02-01', status: 'active' },
    { id: 'emp2', company_id: companyId, first_name: 'Yarisel', last_name: 'Gómez', cedula: '4-725-987', ss_number: '98-76-5432', dv: '41', birth_date: '1990-11-22', age: 35, sex: 'F', marital_status: 'soltera', blood_type: 'A+', nationality: 'Panameña', address: 'Vía Argentina, Ciudad de Panamá', phone: '+507 6788-9900', email: 'yarisel.gomez@demo.com', position: 'Director de RRHH', department: 'Recursos Humanos', branch: 'Sede Principal', salary: 2800.00, weekly_hours: 44, monthly_hours: 190.6, hourly_rate: 14.6905, contract_type: 'indefinido', start_date: '2018-06-15', status: 'active' },
    { id: 'emp3', company_id: companyId, first_name: 'Carlos', last_name: 'Villarreal', cedula: '8-910-1112', ss_number: '22-33-4455', dv: '12', birth_date: '1993-08-04', age: 32, sex: 'M', marital_status: 'soltero', blood_type: 'O-', nationality: 'Panameño', address: 'Brisas del Golf, San Miguelito', phone: '+507 6599-7788', email: 'carlos.v@demo.com', position: 'Desarrollador Full Stack', department: 'Tecnología', branch: 'Sede Principal', salary: 2200.00, weekly_hours: 40, monthly_hours: 173.3, hourly_rate: 12.6947, contract_type: 'indefinido', start_date: '2021-03-01', status: 'active' },
    { id: 'emp4', company_id: companyId, first_name: 'Itzel', last_name: 'Montenegro', cedula: '9-722-104', ss_number: '88-11-2233', dv: '03', birth_date: '1988-02-14', age: 38, sex: 'F', marital_status: 'casada', blood_type: 'B+', nationality: 'Panameña', address: 'Don Bosco, Juan Díaz', phone: '+507 6677-8899', email: 'itzel.m@demo.com', position: 'Contador General', department: 'Contabilidad & Finanzas', branch: 'Sede Principal', salary: 1900.00, weekly_hours: 44, monthly_hours: 190.6, hourly_rate: 9.9685, contract_type: 'indefinido', start_date: '2019-10-01', status: 'active' },
    { id: 'emp5', company_id: companyId, first_name: 'Demetrio', last_name: 'Araúz', cedula: '4-811-222', ss_number: '77-66-5544', dv: '09', birth_date: '1995-09-30', age: 30, sex: 'M', marital_status: 'soltero', blood_type: 'O+', nationality: 'Panameño', address: 'Altos de Cabuya, Tocumen', phone: '+507 6122-3344', email: 'demetrio.arauz@demo.com', position: 'Supervisor de Logística', department: 'Operaciones & Logística', branch: 'Bodega Milla 8', salary: 1250.00, weekly_hours: 48, monthly_hours: 208.0, hourly_rate: 6.0096, contract_type: 'indefinido', start_date: '2022-01-10', status: 'active' },
    
    // Agregamos más empleados para volumen
    { id: 'emp6', company_id: companyId, first_name: 'Milagros', last_name: 'Castillo', cedula: '8-809-563', ss_number: '44-55-6677', dv: '15', birth_date: '1991-04-18', age: 35, sex: 'F', marital_status: 'casada', blood_type: 'AB+', nationality: 'Panameña', address: 'Chorrera, Sector Balboa', phone: '+507 6233-4455', email: 'milagros.c@demo.com', position: 'Analista de Planilla', department: 'Recursos Humanos', branch: 'Sede Principal', salary: 1400.00, weekly_hours: 44, monthly_hours: 190.6, hourly_rate: 7.3452, contract_type: 'indefinido', start_date: '2023-04-01', status: 'active' },
    { id: 'emp7', company_id: companyId, first_name: 'Jorge', last_name: 'Batista', cedula: '2-115-442', ss_number: '33-99-8877', dv: '22', birth_date: '1987-12-05', age: 38, sex: 'M', marital_status: 'casado', blood_type: 'A-', nationality: 'Panameño', address: 'Penonomé, Coclé', phone: '+507 6900-5544', email: 'jorge.b@demo.com', position: 'Asesor Legal Interno', department: 'Administración', branch: 'Sede Principal', salary: 2500.00, weekly_hours: 40, monthly_hours: 173.3, hourly_rate: 14.4258, contract_type: 'indefinido', start_date: '2020-07-01', status: 'active' },
    { id: 'emp8', company_id: companyId, first_name: 'Yamileth', last_name: 'Pinto', cedula: '8-765-432', ss_number: '55-44-3322', dv: '18', birth_date: '1994-06-25', age: 31, sex: 'F', marital_status: 'soltera', blood_type: 'O+', nationality: 'Panameña', address: 'Las Cumbres, Panamá Norte', phone: '+507 6890-1122', email: 'yamileth.p@demo.com', position: 'Desarrollador Front End', department: 'Tecnología', branch: 'Sede Principal', salary: 1800.00, weekly_hours: 40, monthly_hours: 173.3, hourly_rate: 10.3866, contract_type: 'temporal', start_date: '2025-01-15', contract_expiry: '2026-07-15', status: 'active' },
    { id: 'emp9', company_id: companyId, first_name: 'Rigoberto', last_name: 'De León', cedula: '7-122-854', ss_number: '88-44-9911', dv: '80', birth_date: '1983-09-02', age: 42, sex: 'M', marital_status: 'divorciado', blood_type: 'B-', nationality: 'Panameño', address: 'Las Tablas, Los Santos', phone: '+507 6311-2233', email: 'rigo.deleon@demo.com', position: 'Encargado de Compras', department: 'Contabilidad & Finanzas', branch: 'Sede Principal', salary: 1600.00, weekly_hours: 44, monthly_hours: 190.6, hourly_rate: 8.3945, contract_type: 'indefinido', start_date: '2017-11-01', status: 'active' },
    { id: 'emp10', company_id: companyId, first_name: 'Zuleika', last_name: 'Berrío', cedula: '8-802-965', ss_number: '11-77-2266', dv: '02', birth_date: '1996-01-30', age: 30, sex: 'F', marital_status: 'soltera', blood_type: 'O+', nationality: 'Panameña', address: 'Clayton, Ciudad de Panamá', phone: '+507 6533-2211', email: 'zuleika.b@demo.com', position: 'Asistente Administrativo', department: 'Administración', branch: 'Sede Principal', salary: 1000.00, weekly_hours: 44, monthly_hours: 190.6, hourly_rate: 5.2465, contract_type: 'indefinido', start_date: '2023-09-15', status: 'active' },
    { id: 'emp11', company_id: companyId, first_name: 'Juan', last_name: 'Pérez', cedula: '8-999-999', ss_number: '99-99-9999', dv: '99', birth_date: '1999-09-09', age: 26, sex: 'M', marital_status: 'soltero', blood_type: 'A+', nationality: 'Panameño', address: 'Tocumen', phone: '+507 6999-9999', email: 'juan.perez@demo.com', position: 'Auxiliar de Logística', department: 'Operaciones & Logística', branch: 'Bodega Milla 8', salary: 850.00, weekly_hours: 48, monthly_hours: 208.0, hourly_rate: 4.0865, contract_type: 'temporal', start_date: '2025-12-01', contract_expiry: '2026-06-01', status: 'active' },
    { id: 'emp12', company_id: companyId, first_name: 'Ana', last_name: 'López', cedula: '3-712-456', ss_number: '33-33-3333', dv: '33', birth_date: '1992-03-03', age: 34, sex: 'F', marital_status: 'casada', blood_type: 'O+', nationality: 'Panameña', address: 'Colón, Sector Coco Solo', phone: '+507 6333-3333', email: 'ana.lopez@demo.com', position: 'Asistente Administrativo', department: 'Recursos Humanos', branch: 'Sede Principal', salary: 1000.00, weekly_hours: 44, monthly_hours: 190.6, hourly_rate: 5.2465, contract_type: 'indefinido', start_date: '2024-02-01', status: 'active' }
  ];

  // 5. REGISTROS DE ASISTENCIA (Últimos 15 días para los 12 empleados -> ~180 registros)
  const attendance = [];
  const startDay = new Date();
  startDay.setDate(startDay.getDate() - 15);
  
  for (let i = 0; i < 15; i++) {
    const current = new Date(startDay);
    current.setDate(startDay.getDate() + i);
    const dayOfWeek = current.getDay();
    if (dayOfWeek === 0) continue; // Excluir domingos
    
    const dateStr = current.toISOString().split('T')[0];
    
    employees.forEach(emp => {
      let status = 'present';
      let timeIn = '08:00';
      let timeOut = '17:00';
      
      const rand = Math.random();
      if (rand > 0.96) {
        status = 'absent';
        timeIn = null;
        timeOut = null;
      } else if (rand > 0.88) {
        status = 'late';
        timeIn = '08:20';
      }

      if (dayOfWeek === 6) { // Sábado
        timeOut = emp.weekly_hours > 40 ? '12:00' : null;
        if (timeOut === null) status = 'absent';
      }

      if (timeIn !== null || status === 'late' || status === 'present') {
        attendance.push({
          id: `att-${emp.id}-${dateStr}`,
          company_id: companyId,
          employee_id: emp.id,
          employee_name: `${emp.first_name} ${emp.last_name}`,
          department: emp.department,
          date: dateStr,
          time_in: timeIn,
          time_out: timeOut,
          status: status,
          notes: status === 'late' ? 'Tardanza por tráfico regular' : null,
          created_at: new Date().toISOString()
        });
      }
    });
  }

  // 6. BALANCE DE VACACIONES (12 registros)
  const leaveBalances = employees.map((emp, index) => {
    const years = 1 + (index % 5);
    const earned = years * 30;
    const used = Math.round(earned * 0.4);
    return {
      id: `bal-${emp.id}`,
      company_id: companyId,
      employee_id: emp.id,
      employee_name: `${emp.first_name} ${emp.last_name}`,
      year: 2026,
      earned_days: earned,
      used_days: used,
      created_at: new Date().toISOString()
    };
  });

  // 7. SOLICITUDES DE VACACIONES (10 solicitudes)
  const leaveRequests = [
    { id: 'req1', company_id: companyId, employee_id: 'emp3', employee_name: 'Carlos Villarreal', type: 'vacaciones', start_date: '2026-06-01', end_date: '2026-06-15', days: 15, status: 'pending', notes: 'Descanso anual regular', created_at: new Date().toISOString() },
    { id: 'req2', company_id: companyId, employee_id: 'emp5', employee_name: 'Demetrio Araúz', type: 'vacaciones', start_date: '2026-04-10', end_date: '2026-04-15', days: 5, status: 'approved', notes: 'Semana de pascua', created_at: new Date().toISOString() },
    { id: 'req3', company_id: companyId, employee_id: 'emp6', employee_name: 'Milagros Castillo', type: 'vacaciones', start_date: '2026-07-01', end_date: '2026-07-10', days: 10, status: 'pending', notes: 'Viaje familiar', created_at: new Date().toISOString() },
    { id: 'req4', company_id: companyId, employee_id: 'emp8', employee_name: 'Yamileth Pinto', type: 'vacaciones', start_date: '2026-05-10', end_date: '2026-05-18', days: 8, status: 'approved', notes: 'Diligencias personales', created_at: new Date().toISOString() },
    { id: 'req5', company_id: companyId, employee_id: 'emp10', employee_name: 'Zuleika Berrío', type: 'vacaciones', start_date: '2026-08-15', end_date: '2026-08-30', days: 15, status: 'pending', notes: 'Estudios de postgrado', created_at: new Date().toISOString() },
    { id: 'req6', company_id: companyId, employee_id: 'emp2', employee_name: 'Yarisel Gómez', type: 'vacaciones', start_date: '2026-02-01', end_date: '2026-02-10', days: 9, status: 'approved', notes: 'Vacaciones vencidas', created_at: new Date().toISOString() },
    { id: 'req7', company_id: companyId, employee_id: 'emp11', employee_name: 'Juan Pérez', type: 'vacaciones', start_date: '2026-09-01', end_date: '2026-09-07', days: 6, status: 'pending', notes: 'Permiso regular', created_at: new Date().toISOString() },
    { id: 'req8', company_id: companyId, employee_id: 'emp7', employee_name: 'Jorge Batista', type: 'vacaciones', start_date: '2026-03-12', end_date: '2026-03-20', days: 8, status: 'approved', notes: 'Cuestiones médicas familiares', created_at: new Date().toISOString() },
    { id: 'req9', company_id: companyId, employee_id: 'emp12', employee_name: 'Ana López', type: 'vacaciones', start_date: '2026-10-05', end_date: '2026-10-15', days: 10, status: 'pending', notes: 'Aniversario de bodas', created_at: new Date().toISOString() },
    { id: 'req10', company_id: companyId, employee_id: 'emp9', employee_name: 'Rigoberto De León', type: 'vacaciones', start_date: '2026-04-18', end_date: '2026-04-28', days: 10, status: 'approved', notes: 'Asuntos en el interior del país', created_at: new Date().toISOString() }
  ];

  // 8. HORAS EXTRAS (10 registros)
  const overtime = [
    { id: 'ot1', company_id: companyId, employee_id: 'emp5', employee_name: 'Demetrio Araúz', date: new Date().toISOString().split('T')[0], hours: 4.5, type: 'regular', hourly_rate: 6.0096, amount: 33.80, notes: 'Apoyo descarga bodega Milla 8', created_at: new Date().toISOString() },
    { id: 'ot2', company_id: companyId, employee_id: 'emp11', employee_name: 'Juan Pérez', date: new Date().toISOString().split('T')[0], hours: 3.0, type: 'nocturna', hourly_rate: 4.0865, amount: 18.39, notes: 'Cierre inventario mensual', created_at: new Date().toISOString() },
    { id: 'ot3', company_id: companyId, employee_id: 'emp5', employee_name: 'Demetrio Araúz', date: '2026-05-10', hours: 6.0, type: 'festivo', hourly_rate: 6.0096, amount: 63.10, notes: 'Guardia operativa domingo', created_at: new Date().toISOString() },
    { id: 'ot4', company_id: companyId, employee_id: 'emp3', employee_name: 'Carlos Villarreal', date: '2026-05-14', hours: 2.0, type: 'regular', hourly_rate: 12.6947, amount: 31.74, notes: 'Despliegue servidor de base de datos', created_at: new Date().toISOString() },
    { id: 'ot5', company_id: companyId, employee_id: 'emp6', employee_name: 'Milagros Castillo', date: '2026-05-14', hours: 4.0, type: 'regular', hourly_rate: 7.3452, amount: 36.73, notes: 'Cierre quincenal de planilla', created_at: new Date().toISOString() },
    { id: 'ot6', company_id: companyId, employee_id: 'emp9', employee_name: 'Rigoberto De León', date: '2026-05-11', hours: 3.5, type: 'regular', hourly_rate: 8.3945, amount: 36.73, notes: 'Auditoría inventario de compras', created_at: new Date().toISOString() },
    { id: 'ot7', company_id: companyId, employee_id: 'emp8', employee_name: 'Yamileth Pinto', date: '2026-05-08', hours: 2.5, type: 'nocturna', hourly_rate: 10.3866, amount: 38.95, notes: 'Hotfix en producción portal clientes', created_at: new Date().toISOString() },
    { id: 'ot8', company_id: companyId, employee_id: 'emp10', employee_name: 'Zuleika Berrío', date: '2026-05-12', hours: 1.5, type: 'regular', hourly_rate: 5.2465, amount: 9.84, notes: 'Redacción de actas de junta directiva', created_at: new Date().toISOString() },
    { id: 'ot9', company_id: companyId, employee_id: 'emp12', employee_name: 'Ana López', date: '2026-05-13', hours: 2.0, type: 'regular', hourly_rate: 5.2465, amount: 13.12, notes: 'Entrevistas de reclutamiento tarde', created_at: new Date().toISOString() },
    { id: 'ot10', company_id: companyId, employee_id: 'emp11', employee_name: 'Juan Pérez', date: '2026-05-15', hours: 4.0, type: 'regular', hourly_rate: 4.0865, amount: 20.43, notes: 'Organización bodega despacho', created_at: new Date().toISOString() }
  ];

  // 9. DEDUCCIONES / PRÉSTAMOS (10 préstamos)
  const deductions = [
    { id: 'ded1', company_id: companyId, employee_id: 'emp3', employee_name: 'Carlos Villarreal', type: 'prestamo', description: 'Préstamo Financiamiento Laptop', total_amount: 1200.00, quota: 50.00, remaining: 800.00, start_date: '2026-01-15', status: 'active', created_at: new Date().toISOString() },
    { id: 'ded2', company_id: companyId, employee_id: 'emp5', employee_name: 'Demetrio Araúz', type: 'prestamo', description: 'Adelanto de Salario', total_amount: 200.00, quota: 25.00, remaining: 75.00, start_date: '2026-03-01', status: 'active', created_at: new Date().toISOString() },
    { id: 'ded3', company_id: companyId, employee_id: 'emp6', employee_name: 'Milagros Castillo', type: 'prestamo', description: 'Préstamo de Emergencia', total_amount: 500.00, quota: 25.00, remaining: 425.00, start_date: '2026-04-15', status: 'active', created_at: new Date().toISOString() },
    { id: 'ded4', company_id: companyId, employee_id: 'emp8', employee_name: 'Yamileth Pinto', type: 'caja_ahorro', description: 'Caja de Ahorro Interno', total_amount: 2400.00, quota: 100.00, remaining: 1800.00, start_date: '2026-01-01', status: 'active', created_at: new Date().toISOString() },
    { id: 'ded5', company_id: companyId, employee_id: 'emp10', employee_name: 'Zuleika Berrío', type: 'caja_ahorro', description: 'Ahorro Cooperativa', total_amount: 1200.00, quota: 50.00, remaining: 900.00, start_date: '2026-01-01', status: 'active', created_at: new Date().toISOString() },
    { id: 'ded6', company_id: companyId, employee_id: 'emp2', employee_name: 'Yarisel Gómez', type: 'prestamo', description: 'Financiamiento Curso Maestría', total_amount: 3000.00, quota: 125.00, remaining: 2375.00, start_date: '2025-10-01', status: 'active', created_at: new Date().toISOString() },
    { id: 'ded7', company_id: companyId, employee_id: 'emp4', employee_name: 'Itzel Montenegro', type: 'prestamo', description: 'Préstamo Automotriz Co', total_amount: 6000.00, quota: 200.00, remaining: 4800.00, start_date: '2025-06-01', status: 'active', created_at: new Date().toISOString() },
    { id: 'ded8', company_id: companyId, employee_id: 'emp7', employee_name: 'Jorge Batista', type: 'caja_ahorro', description: 'Ahorro Navideño', total_amount: 2400.00, quota: 100.00, remaining: 1900.00, start_date: '2026-01-01', status: 'active', created_at: new Date().toISOString() },
    { id: 'ded9', company_id: companyId, employee_id: 'emp9', employee_name: 'Rigoberto De León', type: 'prestamo', description: 'Préstamo Compra Lentes', total_amount: 250.00, quota: 25.00, remaining: 100.00, start_date: '2026-02-15', status: 'active', created_at: new Date().toISOString() },
    { id: 'ded10', company_id: companyId, employee_id: 'emp12', employee_name: 'Ana López', type: 'caja_ahorro', description: 'Fondo de Retiro Mutuo', total_amount: 1200.00, quota: 50.00, remaining: 950.00, start_date: '2026-01-01', status: 'active', created_at: new Date().toISOString() }
  ];

  // 10. HISTORIAL PLANILLAS (6 quincenas históricas -> ~3 meses)
  const payrollHistory = [
    { id: 'pay1', company_id: companyId, period: '1ra Quincena Marzo 2026', month: 3, year: 2026, month_name: 'Marzo', employee_count: 12, total_bruto: 11950.00, total_neto: 10635.50, total_empresa: 13593.12, created_at: '2026-03-15T12:00:00Z' },
    { id: 'pay2', company_id: companyId, period: '2da Quincena Marzo 2026', month: 3, year: 2026, month_name: 'Marzo', employee_count: 12, total_bruto: 12100.00, total_neto: 10769.00, total_empresa: 13763.75, created_at: '2026-03-30T12:00:00Z' },
    { id: 'pay3', company_id: companyId, period: '1ra Quincena Abril 2026', month: 4, year: 2026, month_name: 'Abril', employee_count: 12, total_bruto: 11950.00, total_neto: 10635.50, total_empresa: 13593.12, created_at: '2026-04-15T12:00:00Z' },
    { id: 'pay4', company_id: companyId, period: '2da Quincena Abril 2026', month: 4, year: 2026, month_name: 'Abril', employee_count: 12, total_bruto: 12450.00, total_neto: 11080.50, total_empresa: 14161.88, created_at: '2026-04-30T12:00:00Z' },
    { id: 'pay5', company_id: companyId, period: '1ra Quincena Mayo 2026', month: 5, year: 2026, month_name: 'Mayo', employee_count: 12, total_bruto: 11950.00, total_neto: 10635.50, total_empresa: 13593.12, created_at: '2026-05-15T12:00:00Z' }
  ];

  // 11. INCAPACIDADES MÉDICAS (10 incapacidades)
  const medicalRecords = [
    { id: 'med1', company_id: companyId, employee_id: 'emp4', employee_name: 'Itzel Montenegro', type: 'Enfermedad común', diagnosis: 'Gastroenteritis Aguda', start_date: '2026-05-02', end_date: '2026-05-05', days: 4, employer_days: 3, css_days: 1, cert_number: 'CSS-992211', notes: 'Validado por médico de cabecera', status: 'completada', created_at: new Date().toISOString() },
    { id: 'med2', company_id: companyId, employee_id: 'emp5', employee_name: 'Demetrio Araúz', type: 'Riesgo profesional', diagnosis: 'Esguince de tobillo grado II', start_date: '2026-03-10', end_date: '2026-03-20', days: 11, employer_days: 3, css_days: 8, cert_number: 'CSS-881144', notes: 'Accidente en patio de maniobras Milla 8', status: 'completada', created_at: new Date().toISOString() },
    { id: 'med3', company_id: companyId, employee_id: 'emp8', employee_name: 'Yamileth Pinto', type: 'Enfermedad común', diagnosis: 'Migraña Crónica', start_date: '2026-04-05', end_date: '2026-04-06', days: 2, employer_days: 2, css_days: 0, cert_number: 'PRV-55441', notes: 'Certificado privado de Clínica Transístmica', status: 'completada', created_at: new Date().toISOString() },
    { id: 'med4', company_id: companyId, employee_id: 'emp10', employee_name: 'Zuleika Berrío', type: 'Enfermedad común', diagnosis: 'Bronquitis Aguda', start_date: '2026-02-12', end_date: '2026-02-17', days: 6, employer_days: 3, css_days: 3, cert_number: 'CSS-66332', notes: 'Guardar reposo y medicación', status: 'completada', created_at: new Date().toISOString() },
    { id: 'med5', company_id: companyId, employee_id: 'emp3', employee_name: 'Carlos Villarreal', type: 'Enfermedad común', diagnosis: 'Faringitis Estreptocócica', start_date: '2026-01-20', end_date: '2026-01-22', days: 3, employer_days: 3, css_days: 0, cert_number: 'CSS-11223', notes: 'Reposo en casa', status: 'completada', created_at: new Date().toISOString() },
    { id: 'med6', company_id: companyId, employee_id: 'emp6', employee_name: 'Milagros Castillo', type: 'Enfermedad común', diagnosis: 'Lumbago Agudo', start_date: '2026-03-01', end_date: '2026-03-04', days: 4, employer_days: 3, css_days: 1, cert_number: 'CSS-44556', notes: 'Derivado a fisioterapia', status: 'completada', created_at: new Date().toISOString() },
    { id: 'med7', company_id: companyId, employee_id: 'emp11', employee_name: 'Juan Pérez', type: 'Enfermedad común', diagnosis: 'Dermatitis por Contacto', start_date: '2026-04-14', end_date: '2026-04-16', days: 3, employer_days: 3, css_days: 0, cert_number: 'CSS-00223', notes: 'Uso de guantes obligatorio', status: 'completada', created_at: new Date().toISOString() },
    { id: 'med8', company_id: companyId, employee_id: 'emp12', employee_name: 'Ana López', type: 'Enfermedad común', diagnosis: 'Cefalea Tensional', start_date: '2026-05-18', end_date: '2026-05-19', days: 2, employer_days: 2, css_days: 0, cert_number: 'CSS-88772', notes: 'Reposo absoluto', status: 'completada', created_at: new Date().toISOString() },
    { id: 'med9', company_id: companyId, employee_id: 'emp9', employee_name: 'Rigoberto De León', type: 'Enfermedad común', diagnosis: 'Infección Urinaria', start_date: '2026-02-05', end_date: '2026-02-08', days: 4, employer_days: 3, css_days: 1, cert_number: 'CSS-33445', notes: 'Antibioticoterapia activa', status: 'completada', created_at: new Date().toISOString() },
    { id: 'med10', company_id: companyId, employee_id: 'emp2', employee_name: 'Yarisel Gómez', type: 'Enfermedad común', diagnosis: 'Fiebre Dengue', start_date: '2026-04-20', end_date: '2026-04-30', days: 11, employer_days: 3, css_days: 8, cert_number: 'CSS-77889', notes: 'Reposo e hidratación activa', status: 'completada', created_at: new Date().toISOString() }
  ];

  // 12. EVALUACIONES DE DESEMPEÑO (10 evaluaciones)
  const evaluations = [
    { id: 'eval1', company_id: companyId, employee_id: 'emp4', employee_name: 'Itzel Montenegro', period: '1er Trimestre 2026', evaluator: 'Yarisel Gómez', scores: { 'Puntualidad': 5, 'Trabajo en Equipo': 4, 'Productividad': 5, 'Liderazgo': 3 }, avg: 4.25, category: 'Sobresaliente', comments: 'Excelente enfoque en auditoría contable quincenal.', status: 'completada', created_at: new Date().toISOString() },
    { id: 'eval2', company_id: companyId, employee_id: 'emp3', employee_name: 'Carlos Villarreal', period: '1er Trimestre 2026', evaluator: 'Anel Rodríguez', scores: { 'Puntualidad': 4, 'Trabajo en Equipo': 5, 'Productividad': 5, 'Liderazgo': 4 }, avg: 4.50, category: 'Excelente', comments: 'Lideró la migración del servidor Supabase sin caídas de servicio.', status: 'completada', created_at: new Date().toISOString() },
    { id: 'eval3', company_id: companyId, employee_id: 'emp5', employee_name: 'Demetrio Araúz', period: '1er Trimestre 2026', evaluator: 'Yarisel Gómez', scores: { 'Puntualidad': 5, 'Trabajo en Equipo': 4, 'Productividad': 3, 'Liderazgo': 3 }, avg: 3.75, category: 'Aceptable', comments: 'Puntual, requiere optimizar la velocidad en bodega.', status: 'completada', created_at: new Date().toISOString() },
    { id: 'eval4', company_id: companyId, employee_id: 'emp6', employee_name: 'Milagros Castillo', period: '1er Trimestre 2026', evaluator: 'Yarisel Gómez', scores: { 'Puntualidad': 4, 'Trabajo en Equipo': 4, 'Productividad': 5, 'Liderazgo': 4 }, avg: 4.25, category: 'Sobresaliente', comments: 'Procesamiento de planilla 100% exacto siempre.', status: 'completada', created_at: new Date().toISOString() },
    { id: 'eval5', company_id: companyId, employee_id: 'emp8', employee_name: 'Yamileth Pinto', period: '1er Trimestre 2026', evaluator: 'Carlos Villarreal', scores: { 'Puntualidad': 4, 'Trabajo en Equipo': 5, 'Productividad': 4, 'Liderazgo': 3 }, avg: 4.00, category: 'Sobresaliente', comments: 'Muy activa en resolver tickets de soporte técnico.', status: 'completada', created_at: new Date().toISOString() },
    { id: 'eval6', company_id: companyId, employee_id: 'emp10', employee_name: 'Zuleika Berrío', period: '1er Trimestre 2026', evaluator: 'Anel Rodríguez', scores: { 'Puntualidad': 5, 'Trabajo en Equipo': 3, 'Productividad': 4, 'Liderazgo': 3 }, avg: 3.75, category: 'Aceptable', comments: 'Buena redacción y soporte a la junta directiva.', status: 'completada', created_at: new Date().toISOString() },
    { id: 'eval7', company_id: companyId, employee_id: 'emp12', employee_name: 'Ana López', period: '1er Trimestre 2026', evaluator: 'Yarisel Gómez', scores: { 'Puntualidad': 4, 'Trabajo en Equipo': 4, 'Productividad': 4, 'Liderazgo': 3 }, avg: 3.75, category: 'Aceptable', comments: 'Apoyo constante en reclutamientos masivos.', status: 'completada', created_at: new Date().toISOString() },
    { id: 'eval8', company_id: companyId, employee_id: 'emp9', employee_name: 'Rigoberto De León', period: '1er Trimestre 2026', evaluator: 'Itzel Montenegro', scores: { 'Puntualidad': 5, 'Trabajo en Equipo': 4, 'Productividad': 4, 'Liderazgo': 4 }, avg: 4.25, category: 'Sobresaliente', comments: 'Excelente manejo presupuestario con proveedores.', status: 'completada', created_at: new Date().toISOString() },
    { id: 'eval9', company_id: companyId, employee_id: 'emp11', employee_name: 'Juan Pérez', period: '1er Trimestre 2026', evaluator: 'Demetrio Araúz', scores: { 'Puntualidad': 3, 'Trabajo en Equipo': 4, 'Productividad': 3, 'Liderazgo': 2 }, avg: 3.00, category: 'Aceptable', comments: 'Esforzado, requiere mejorar la asistencia.', status: 'completada', created_at: new Date().toISOString() },
    { id: 'eval10', company_id: companyId, employee_id: 'emp7', employee_name: 'Jorge Batista', period: '1er Trimestre 2026', evaluator: 'Anel Rodríguez', scores: { 'Puntualidad': 5, 'Trabajo en Equipo': 4, 'Productividad': 4, 'Liderazgo': 4 }, avg: 4.25, category: 'Sobresaliente', comments: 'Excelente resolución de litigios laborales.', status: 'completada', created_at: new Date().toISOString() }
  ];

  // 13. UNIFORMES / EQUIPOS (12 registros)
  const uniforms = [
    { id: 'uni1', company_id: companyId, employee_id: 'emp5', employee_name: 'Demetrio Araúz', item: 'Bota Industrial de Seguridad', category: 'Calzado', size: '41', quantity: 1, date: '2026-02-10', value: 45.00, notes: 'Asignado para bodega Milla 8.', status: 'activo', created_at: new Date().toISOString() },
    { id: 'uni2', company_id: companyId, employee_id: 'emp11', employee_name: 'Juan Pérez', item: 'Chaleco reflectivo verde', category: 'EPP', size: 'L', quantity: 1, date: '2026-01-15', value: 12.00, notes: 'Trabajo nocturno operativo.', status: 'activo', created_at: new Date().toISOString() },
    { id: 'uni3', company_id: companyId, employee_id: 'emp5', employee_name: 'Demetrio Araúz', item: 'Casco de protección azul', category: 'EPP', size: 'Única', quantity: 1, date: '2026-01-15', value: 15.00, notes: 'Uso obligatorio en patio.', status: 'activo', created_at: new Date().toISOString() },
    { id: 'uni4', company_id: companyId, employee_id: 'emp3', employee_name: 'Carlos Villarreal', item: 'Camisa Polo Bordada Logo', category: 'Uniforme', size: 'M', quantity: 3, date: '2026-01-10', value: 20.00, notes: 'Polos oficiales tecnología.', status: 'activo', created_at: new Date().toISOString() },
    { id: 'uni5', company_id: companyId, employee_id: 'emp6', employee_name: 'Milagros Castillo', item: 'Camisa Polo Bordada Logo', category: 'Uniforme', size: 'S', quantity: 3, date: '2026-01-10', value: 20.00, notes: 'Polos oficiales RRHH.', status: 'activo', created_at: new Date().toISOString() },
    { id: 'uni6', company_id: companyId, employee_id: 'emp10', employee_name: 'Zuleika Berrío', item: 'Camisa Polo Bordada Logo', category: 'Uniforme', size: 'S', quantity: 3, date: '2026-01-10', value: 20.00, notes: 'Polos oficiales administración.', status: 'activo', created_at: new Date().toISOString() },
    { id: 'uni7', company_id: companyId, employee_id: 'emp2', employee_name: 'Yarisel Gómez', item: 'Camisa Polo Bordada Logo', category: 'Uniforme', size: 'M', quantity: 3, date: '2026-01-10', value: 20.00, notes: 'Polos oficiales RRHH.', status: 'activo', created_at: new Date().toISOString() },
    { id: 'uni8', company_id: companyId, employee_id: 'emp4', employee_name: 'Itzel Montenegro', item: 'Camisa Polo Bordada Logo', category: 'Uniforme', size: 'M', quantity: 3, date: '2026-01-10', value: 20.00, notes: 'Polos oficiales finanzas.', status: 'activo', created_at: new Date().toISOString() },
    { id: 'uni9', company_id: companyId, employee_id: 'emp1', employee_name: 'Anel Rodríguez', item: 'Saco Formal Ejecutivo', category: 'Uniforme', size: 'L', quantity: 1, date: '2026-01-05', value: 120.00, notes: 'Uniforme de gala ejecutiva.', status: 'activo', created_at: new Date().toISOString() },
    { id: 'uni10', company_id: companyId, employee_id: 'emp7', employee_name: 'Jorge Batista', item: 'Camisa Polo Bordada Logo', category: 'Uniforme', size: 'L', quantity: 3, date: '2026-01-10', value: 20.00, notes: 'Polos oficiales legal.', status: 'activo', created_at: new Date().toISOString() },
    { id: 'uni11', company_id: companyId, employee_id: 'emp8', employee_name: 'Yamileth Pinto', item: 'Camisa Polo Bordada Logo', category: 'Uniforme', size: 'S', quantity: 3, date: '2026-01-10', value: 20.00, notes: 'Polos oficiales tecnología.', status: 'activo', created_at: new Date().toISOString() },
    { id: 'uni12', company_id: companyId, employee_id: 'emp9', employee_name: 'Rigoberto De León', item: 'Camisa Polo Bordada Logo', category: 'Uniforme', size: 'L', quantity: 3, date: '2026-01-10', value: 20.00, notes: 'Polos oficiales compras.', status: 'activo', created_at: new Date().toISOString() }
  ];

  // 14. CAPACITACIONES E INSCRIPCIONES (5 cursos y 12 inscripciones)
  const trainings = [
    { id: 'train1', company_id: companyId, name: 'Seminario: Actualización del Código de Trabajo de Panamá', category: 'Legal', hours: 12, start_date: '2026-06-10', end_date: '2026-06-12', mode: 'presencial', instructor: 'Dr. Jaime Barroso (Asociación de Abogados)', description: 'Análisis detallado sobre cálculo de liquidaciones y Art. 54 de vacaciones.', status: 'programado', created_at: new Date().toISOString() },
    { id: 'train2', company_id: companyId, name: 'Taller: Reanimación Cardiopulmonar (RCP) y EPP', category: 'Seguridad', hours: 8, start_date: '2026-05-18', end_date: '2026-05-19', mode: 'presencial', instructor: 'Cuerpo de Bomberos de Panamá', description: 'Primeros auxilios básicos obligatorios para supervisores.', status: 'completado', created_at: new Date().toISOString() },
    { id: 'train3', company_id: companyId, name: 'Metodología Scrum Avanzada', category: 'Tecnología', hours: 24, start_date: '2026-04-05', end_date: '2026-04-15', mode: 'virtual', instructor: 'TechAcademy Latam', description: 'Coordinación ágil de equipos tecnológicos.', status: 'completado', created_at: new Date().toISOString() },
    { id: 'train4', company_id: companyId, name: 'Excel Financiero Avanzado y Macros', category: 'Finanzas', hours: 20, start_date: '2026-03-10', end_date: '2026-03-24', mode: 'virtual', instructor: 'Contadores Asociados S.A.', description: 'Macros contables y modelaje de planillas.', status: 'completado', created_at: new Date().toISOString() },
    { id: 'train5', company_id: companyId, name: 'Liderazgo y Clima de Trabajo de Alta Confianza', category: 'Recursos Humanos', hours: 16, start_date: '2026-07-15', end_date: '2026-07-18', mode: 'presencial', instructor: 'Great Place to Work Panamá', description: 'Estrategias de clima laboral y retención.', status: 'programado', created_at: new Date().toISOString() }
  ];
  
  const enrollments = [
    { id: 'enr1', company_id: companyId, training_id: 'train1', training_name: 'Seminario: Actualización del Código de Trabajo de Panamá', employee_id: 'emp2', employee_name: 'Yarisel Gómez', status: 'inscrito', score: null, notes: 'Mandatorio para Directiva de RRHH', created_at: new Date().toISOString() },
    { id: 'enr2', company_id: companyId, training_id: 'train2', training_name: 'Taller: Reanimación Cardiopulmonar (RCP) y EPP', employee_id: 'emp5', employee_name: 'Demetrio Araúz', status: 'completado', score: 98.0, notes: 'Aprobado con distinción', created_at: new Date().toISOString() },
    { id: 'enr3', company_id: companyId, training_id: 'train3', training_name: 'Metodología Scrum Avanzada', employee_id: 'emp3', employee_name: 'Carlos Villarreal', status: 'completado', score: 95.0, notes: 'Excelente participación', created_at: new Date().toISOString() },
    { id: 'enr4', company_id: companyId, training_id: 'train3', training_name: 'Metodología Scrum Avanzada', employee_id: 'emp8', employee_name: 'Yamileth Pinto', status: 'completado', score: 90.0, notes: 'Completado exitosamente', created_at: new Date().toISOString() },
    { id: 'enr5', company_id: companyId, training_id: 'train4', training_name: 'Excel Financiero Avanzado y Macros', employee_id: 'emp4', employee_name: 'Itzel Montenegro', status: 'completado', score: 100.0, notes: 'Examen final perfecto', created_at: new Date().toISOString() },
    { id: 'enr6', company_id: companyId, training_id: 'train4', training_name: 'Excel Financiero Avanzado y Macros', employee_id: 'emp6', employee_name: 'Milagros Castillo', status: 'completado', score: 92.5, notes: 'Planillas automatizadas logradas', created_at: new Date().toISOString() },
    { id: 'enr7', company_id: companyId, training_id: 'train1', training_name: 'Seminario: Actualización del Código de Trabajo de Panamá', employee_id: 'emp6', employee_name: 'Milagros Castillo', status: 'inscrito', score: null, notes: 'Cálculo de liquidaciones', created_at: new Date().toISOString() },
    { id: 'enr8', company_id: companyId, training_id: 'train1', training_name: 'Seminario: Actualización del Código de Trabajo de Panamá', employee_id: 'emp7', employee_name: 'Jorge Batista', status: 'inscrito', score: null, notes: 'Asesoría legal corporativa', created_at: new Date().toISOString() },
    { id: 'enr9', company_id: companyId, training_id: 'train2', training_name: 'Taller: Reanimación Cardiopulmonar (RCP) y EPP', employee_id: 'emp11', employee_name: 'Juan Pérez', status: 'completado', score: 88.0, notes: 'Completado con éxito', created_at: new Date().toISOString() },
    { id: 'enr10', company_id: companyId, training_id: 'train5', training_name: 'Liderazgo y Clima de Trabajo de Alta Confianza', employee_id: 'emp2', employee_name: 'Yarisel Gómez', status: 'inscrito', score: null, notes: 'Gestión de clima interno', created_at: new Date().toISOString() },
    { id: 'enr11', company_id: companyId, training_id: 'train5', training_name: 'Liderazgo y Clima de Trabajo de Alta Confianza', employee_id: 'emp1', employee_name: 'Anel Rodríguez', status: 'inscrito', score: null, notes: 'Alineamiento ejecutivo', created_at: new Date().toISOString() },
    { id: 'enr12', company_id: companyId, training_id: 'train5', training_name: 'Liderazgo y Clima de Trabajo de Alta Confianza', employee_id: 'emp10', employee_name: 'Zuleika Berrío', status: 'inscrito', score: null, notes: 'Desarrollo de habilidades directivas', created_at: new Date().toISOString() }
  ];

  // 15. ENCUESTAS & RESPUESTAS (1 encuesta activa y 15 respuestas)
  const surveys = [
    {
      id: 'surv1',
      company_id: companyId,
      title: 'Encuesta Anual de Clima Laboral 2026',
      description: 'Conocer la satisfacción del equipo y oportunidades de mejora.',
      audience: 'todos',
      close_date: '2026-06-30',
      status: 'activa',
      questions: [
        { id: 'q1', type: 'scale', text: '¿Te sentís valorado en la empresa?' },
        { id: 'q2', type: 'yesno', text: '¿Tenés las herramientas necesarias para tu labor?' },
        { id: 'q3', type: 'text', text: '¿Qué sugerencia tenés para mejorar la oficina?' }
      ],
      created_at: new Date().toISOString()
    }
  ];
  
  const surveyResponses = [];
  const suggestions = [
    'Mejorar la velocidad del internet.',
    'Acondicionar el aire de la sala de reuniones.',
    'Excelente clima de trabajo.',
    'Más capacitaciones presenciales.',
    'Polos de algodón de mejor calidad.',
    'Cafetería con más opciones saludables.',
    'Muy conforme con la gerencia.',
    'Soporte técnico más ágil.',
    'Caja de ahorro súper útil.',
    'Cerrar temprano los viernes alternos.',
    'Mejorar la ventilación en bodega Milla 8.',
    'Súper contento con las oportunidades.',
    'Más actividades recreativas de integración.',
    'Excelentes diplomados corporativos.',
    'Seguir con los asuetos feriados quincenales.'
  ];

  for (let i = 0; i < 15; i++) {
    surveyResponses.push({
      id: `resp-${i}`,
      company_id: companyId,
      survey_id: 'surv1',
      answers: {
        'q1': 3 + (i % 3), // Genera respuestas 3, 4 y 5
        'q2': i % 4 === 0 ? 'No' : 'Si',
        'q3': suggestions[i]
      },
      created_at: new Date().toISOString()
    });
  }

  // 16. COMUNICADOS (8 comunicados oficiales)
  const announcements = [
    { id: 'ann1', company_id: companyId, title: '📢 Medidas Especiales de Bioseguridad y Jornada de Vacunación CSS', type: 'seguridad', audience: 'todos', content: 'Estimados colaboradores: El próximo martes tendremos una jornada especial de vacunación contra la influenza patrocinada por el CSS en nuestra enfermería. La asistencia es voluntaria.', priority: 'importante', expires: '2026-06-03', views: 18, status: 'activo', created_at: new Date().toISOString() },
    { id: 'ann2', company_id: companyId, title: '🇵🇦 Feriados Nacionales y Cierre por Fiestas Patrias', type: 'general', audience: 'todos', content: 'Les informamos que con motivo de las Fiestas Patrias de Panamá, nuestras oficinas permanecerán cerradas los días 3, 5 y 10 de noviembre. Los recargos correspondientes a guardias operativas se liquidarán en la quincena subsiguiente.', priority: 'normal', expires: '2026-11-12', views: 24, status: 'activo', created_at: '2026-05-15T15:00:00Z' },
    { id: 'ann3', company_id: companyId, title: '📊 Lanzamiento del Plan de Desempeño Trimestral', type: 'general', audience: 'todos', content: 'Iniciamos la fase de autoevaluaciones correspondientes al periodo actual. Se solicita a todos los jefes de departamento programar sus reuniones.', priority: 'normal', expires: '2026-06-15', views: 14, status: 'activo', created_at: new Date().toISOString() },
    { id: 'ann4', company_id: companyId, title: '👔 Entrega de Nuevos Uniformes y EPP', type: 'general', audience: 'todos', content: 'A partir del próximo lunes se iniciará el despacho de los nuevos uniformes en bodega Milla 8. Favor coordinar sus tallas con Yarisel Gómez.', priority: 'normal', expires: '2026-06-10', views: 20, status: 'activo', created_at: new Date().toISOString() },
    { id: 'ann5', company_id: companyId, title: '🎓 Inscripciones Abiertas: Seminario del Código de Trabajo', type: 'evento', audience: 'todos', content: 'Se informa que tenemos vacantes de inscripción disponibles para el seminario laboral del Dr. Jaime Barroso. Inscríbase en la sección de Capacitaciones.', priority: 'normal', expires: '2026-06-05', views: 16, status: 'activo', created_at: new Date().toISOString() },
    { id: 'ann6', company_id: companyId, title: '⚠️ Simulacro de Evacuación y Plan de Contingencias', type: 'seguridad', audience: 'todos', content: 'Se realizará un simulacro de evacuación inopinado durante la próxima semana. Favor repasar la ubicación de las salidas de emergencia.', priority: 'importante', expires: '2026-06-10', views: 30, status: 'activo', created_at: new Date().toISOString() },
    { id: 'ann7', company_id: companyId, title: '🎉 Cumpleaños y Convivio Fin de Mes', type: 'general', audience: 'todos', content: 'Los invitamos a nuestro convivio mensual de cumpleaños este viernes a las 4:00 PM en el comedor principal. ¡Tendremos dulces y refrigerios!', priority: 'normal', expires: '2026-05-31', views: 28, status: 'activo', created_at: new Date().toISOString() },
    { id: 'ann8', company_id: companyId, title: '💻 Campaña de Phishing y Ciberseguridad', type: 'seguridad', audience: 'todos', content: 'Por favor, absténgase de hacer clic en enlaces o descargar adjuntos de remitentes externos sospechosos. Reporte correos maliciosos a soporte.', priority: 'importante', expires: '2026-06-15', views: 22, status: 'activo', created_at: new Date().toISOString() }
  ];

  // ==========================================
  // INYECCIÓN AL ESTADO CENTRAL (MUTACIONES)
  // ==========================================
  State.set('companies', companies);
  State.set('departments', departments);
  State.set('positions', positions);
  State.set('employees', employees);
  State.set('attendance', attendance);
  State.set('leaveBalances', leaveBalances);
  State.set('leaveRequests', leaveRequests);
  State.set('overtime', overtime);
  State.set('deductions', deductions);
  State.set('payrollHistory', payrollHistory);
  State.set('medicalRecords', medicalRecords);
  State.set('evaluations', evaluations);
  State.set('uniforms', uniforms);
  State.set('trainings', trainings);
  State.set('enrollments', enrollments);
  State.set('surveys', surveys);
  State.set('surveyResponses', surveyResponses);
  State.set('announcements', announcements);

  console.log('✅ Base de datos Demo expandida cargada exitosamente.');
}
export default { initDemoData };
