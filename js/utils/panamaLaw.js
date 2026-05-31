/**
 * =============================================================
 * LEYES LABORALES DE PANAMÁ — MOTOR MATEMÁTICO
 * =============================================================
 * Cálculos técnicos y deducciones legales basados en el Código
 * de Trabajo de la República de Panamá y Ley 51 de la CSS.
 */

export const PanamaLaw = {
  // Constantes de Ley
  RATES: {
    CSS_EMPLOYEE: 0.0975,   // Seguro Social Colaborador: 9.75%
    CSS_EMPLOYER: 0.1225,   // Seguro Social Patrono: 12.25% (Riesgos Profesionales promedio excluidos para simplificación)
    SE_EMPLOYEE: 0.0125,    // Seguro Educativo Colaborador: 1.25%
    SE_EMPLOYER: 0.0150,    // Seguro Educativo Patrono: 1.50%
  },

  /**
   * Calcula el desglose de planilla quincenal o mensual para un colaborador
   * @param {number} baseSalary Salario base devengado en el período
   * @param {number} overtimeAmount Monto ganado en concepto de horas extras
   * @param {number} bonusAmount Bonificaciones o comisiones
   * @param {number} customDeductions Suma de otras deducciones personales (préstamos, etc.)
   * @returns {object} Objeto con todo el desglose matemático
   */
  calculatePayroll(baseSalary, overtimeAmount = 0, bonusAmount = 0, customDeductions = 0) {
    const grossSalary = Number(baseSalary) + Number(overtimeAmount) + Number(bonusAmount);
    
    // Retenciones del Colaborador
    const cssEmployee = grossSalary * this.RATES.CSS_EMPLOYEE;
    const seEmployee = grossSalary * this.RATES.SE_EMPLOYEE;
    
    // Impuesto sobre la Renta (ISR) - Simplificado para cálculo mensual/quincenal
    // Rango exento en Panamá: hasta $11,000 anuales ($916.66 mensual)
    const isr = this.calculateISR(grossSalary * 24); // Proyectado anual (asumiendo planilla quincenal) / 24 quincenas
    
    const totalDeductions = cssEmployee + seEmployee + isr + Number(customDeductions);
    const netSalary = grossSalary - totalDeductions;

    // Aportes Patronales
    const cssEmployer = grossSalary * this.RATES.CSS_EMPLOYER;
    const seEmployer = grossSalary * this.RATES.SE_EMPLOYER;
    const totalEmployerCost = grossSalary + cssEmployer + seEmployer;

    return {
      grossSalary: this.round(grossSalary),
      cssEmployee: this.round(cssEmployee),
      seEmployee: this.round(seEmployee),
      isr: this.round(isr),
      totalDeductions: this.round(totalDeductions),
      netSalary: this.round(netSalary),
      
      // Costos patrono
      cssEmployer: this.round(cssEmployer),
      seEmployer: this.round(seEmployer),
      totalEmployerCost: this.round(totalEmployerCost)
    };
  },

  /**
   * Calcula el Impuesto sobre la Renta (ISR) en Panamá proyectado quincenalmente
   * Tarifas anuales:
   * - Hasta $11,000.00: 0% (Exento)
   * - De $11,000.00 a $50,000.00: 15% sobre el excedente de $11,000
   * - Más de $50,000.00: $5,850.00 por los primeros $50,000, y 25% sobre el excedente
   */
  calculateISR(annualSalary) {
    let annualISR = 0;
    if (annualSalary > 50000) {
      annualISR = 5850 + (annualSalary - 50000) * 0.25;
    } else if (annualSalary > 11000) {
      annualISR = (annualSalary - 11000) * 0.15;
    }
    
    // Retorna el ISR correspondiente a una quincena (24 quincenas al año)
    return annualISR / 24;
  },

  /**
   * Calcula el recargo de Horas Extras según el Código de Trabajo
   * @param {number} hourlyRate Tarifa por hora del colaborador
   * @param {number} hours Cantidad de horas extras trabajadas
   * @param {string} type Tipo de recargo: 'regular' (+25%), 'nocturna' (+50%), 'festivo' (+75%)
   */
  calculateOvertime(hourlyRate, hours, type = 'regular') {
    let rateFactor = 1.25; // Diurna (+25%)
    
    if (type === 'nocturna') {
      rateFactor = 1.50; // Nocturna (+50%)
    } else if (type === 'festivo') {
      rateFactor = 1.75; // Festivos / Domingos / Días Nacionales (+75%)
    }
    
    const amount = Number(hourlyRate) * Number(hours) * rateFactor;
    return this.round(amount);
  },

  /**
   * Calcula la acumulación matemática de Vacaciones (Art. 54)
   * 1 día de descanso por cada 11 días trabajados (proporción 9.09% o 1/11)
   */
  calculateAccruedVacations(daysWorked) {
    return Math.floor(daysWorked / 11);
  },

  /**
   * Calcula el Décimo Tercer Mes acumulado quincenalmente
   * Consiste en un día de salario por cada 11 días de trabajo, pagadero en tres partidas
   * (15 de abril, 15 de agosto, 15 de diciembre). Se calcula sobre el salario bruto.
   */
  calculateThirteenthMonth(grossSalaryPeriod) {
    // Proporcional del período: Salario Bruto / 12
    return this.round(grossSalaryPeriod / 12);
  },

  /**
   * Calcula las indemnizaciones y prima de antigüedad para liquidación (Despido injustificado o renuncia)
   * @param {string} startDate Fecha de ingreso
   * @param {string} endDate Fecha de salida
   * @param {number} avgSalary Promedio de salarios mensuales devengados en el último año (o últimos 5)
   * @param {string} reason Motivo de salida: 'mutuo', 'despido_justificado', 'despido_injustificado', 'renuncia'
   * @param {number} accruedVacationDays Saldo de vacaciones pendientes
   */
  calculateLiquidation(startDate, endDate, avgSalary, reason = 'renuncia', accruedVacationDays = 0) {
    const start = new Date(startDate);
    const end = new Date(endDate);
    
    // Cálculo exacto del tiempo de servicio (Años y Meses)
    const diffTime = Math.abs(end - start);
    const diffDays = Math.ceil(diffTime / (1000 * 60 * 60 * 24));
    const yearsService = diffDays / 365.25;

    // 1. Vacaciones Proporcionales pendientes de pago
    const vacationPay = (avgSalary / 30) * accruedVacationDays;

    // 2. Décimo Tercer Mes Proporcional (desde la última fecha de pago de partida)
    // Para simplificar, estimamos 2 meses de acumulación promedio ($avgSalary / 12 * 2)
    const thirteenthPay = (avgSalary / 12) * 2; 

    // 3. Prima de Antigüedad (Art. 224 Código de Trabajo)
    // 1 semana de salario por cada año de servicio.
    // Fórmula: 1.92% de la suma de todos los salarios devengados.
    const totalWagesEarned = avgSalary * 12 * yearsService;
    const seniorityBonus = totalWagesEarned * 0.0192;

    // 4. Indemnización (Art. 225) - Solo aplica si es Despido Injustificado
    let indemnity = 0;
    if (reason === 'despido_injustificado') {
      if (yearsService < 1) {
        // 1 semana por cada 3 meses
        indemnity = (avgSalary / 4.33) * (yearsService * 4);
      } else {
        // Escala progresiva: 3.4 semanas por cada año de servicios (primeros 10 años)
        const yearsForIndemnity = Math.min(yearsService, 10);
        indemnity = (avgSalary / 4.33) * 3.4 * yearsForIndemnity;
        
        // Años adicionales: 1 semana adicional por año
        if (yearsService > 10) {
          indemnity += (avgSalary / 4.33) * (yearsService - 10);
        }
      }
    }

    const subtotal = vacationPay + thirteenthPay + seniorityBonus + indemnity;

    return {
      yearsService: this.round(yearsService),
      diffDays,
      vacationPay: this.round(vacationPay),
      thirteenthPay: this.round(thirteenthPay),
      seniorityBonus: this.round(seniorityBonus),
      indemnity: this.round(indemnity),
      totalLiquidation: this.round(subtotal)
    };
  },

  /**
   * Helper para redondear a dos decimales de forma financiera
   */
  round(num) {
    return Math.round((num + Number.EPSILON) * 100) / 100;
  }
};
