# Changelog — HR Admin Panel

**Repositorio:** [github.com/livant05/hr-admin-panel-v5](https://github.com/livant05/hr-admin-panel-v5)


## v5.1 (05/31/2026)
### Correcciones (Auditoría)
- 🐛 Recibo de pago: Horas Extras ahora retiene CSS 9.75% + SE 1.25% + ISR — antes se sumaban brutas al neto (error de cumplimiento fiscal)
- 🐛 Renombrado "13er Mes" → "Décimo 3er Mes" / "Décimo" en todos los módulos: tabla de planilla, recibo de pago, export CSV, impresión de planilla
- 🐛 CSV export de planilla: columna ISR agregada al encabezado (faltaba tras el cambio anterior)

### Mejoras de Módulos
- 💼 Liquidación: retenciones legales por componente según Ley panameña
  - Vacaciones proporcionales: CSS 9.75% + SE 1.25% + ISR (tasa efectiva anual)
  - Décimo 3er Mes: CSS especial 7.25% + SE 1.25% — exento de ISR
  - Prima de antigüedad e indemnización: exentas de CSS, SE e ISR
  - Preaviso: CSS 9.75% + SE 1.25% + ISR
  - Base legal: Arts. 225-230 C. Trabajo · Ley 51/2005 CSS · Código Fiscal Panamá
  - Resultado muestra secciones: Devengado Bruto, Retenciones Legales, Neto a Pagar
  - PDF con desglose completo de retenciones por concepto
- 💼 Liquidación: deducciones activas del empleado aparecen como checkboxes
  - Préstamos, embargos y caja de ahorro se pueden activar/desactivar individualmente
  - Total neto se recalcula en tiempo real al marcar/desmarcar
  - PDF imprimible incluye desglose de deducciones descontadas
- 💼 Liquidación: "13er Mes" renombrado a "Décimo 3er Mes" en toda la sección

### Cálculo Fiscal
- 🧾 ISR — Impuesto Sobre la Renta agregado a Planilla y Recibo de Pago
  - Método de proyección anual: salario × 12 → tramos → ÷ 12 mensual
  - Tramos: 0% hasta B/. 11,000 · 15% hasta B/. 50,000 · 25% sobre el exceso
  - Columna ISR en tabla de planilla con totales
  - Línea ISR en sección Deducciones Legales del recibo de pago imprimible
  - Campo `total_isr` guardado en historial de planillas

### Módulo Agregado
- 👔 Reclutamiento y Selección (vacantes, candidatos, pipeline de selección)
  - Gestión de vacantes con rango salarial y estado (Abierta / Pausada / Cerrada)
  - Base de datos de candidatos con perfil y notas de CV
  - Pipeline por etapas: Postulado → Revisión → Entrevista → Oferta → Contratado / Rechazado
  - Indicador de Ley 1/1986 — Regla del 90% panameños en tiempo real

### Mejoras de UI
- Sidebar: header de sección EMPLEADOS agregado
- Sidebar: ícono de Reclutamiento diferenciado (`fa-user-tie`)
- Sidebar: "Calcular Planilla" → "Planilla" (consistencia de labels)
- Sidebar: "Uniformes/Equipos" → "Uniformes" (label limpio)
- Sidebar: espaciado vertical reducido — sidebar completo sin scroll
- Sidebar: header "DOCUMENTOS Y REPORTES" → "DOCUMENTOS" (evita overflow)
- Sidebar: ítem "Nuevo Empleado" eliminado (acción redundante, disponible dentro de Empleados)
- Sidebar: secciones colapsables con chevron — click en header para expandir/contraer grupo
- Sidebar: barra de desplazamiento vertical propia (altura fija `calc(100vh - 57px)`, scroll independiente)
- Sidebar: modo mini con íconos — botón ☰ colapsa a 74px mostrando solo íconos con tooltip al hover
- Sidebar: nombre de empresa reemplaza "HR Admin" en la cabecera — se actualiza al login y al cambiar el nombre en Configuración

## v5.0 (05/14/2026)
### Módulos Extra Agregados
- 📊 Evaluaciones de Desempeño (8 criterios, 5 categorías)
- 👔 Control de Uniformes y Equipos
- 🏥 Control de Incapacidades Médicas (Ley CSS Panamá)
- 📋 Encuestas de Clima Laboral (escala, texto, Sí/No)
- 🎓 Capacitaciones y Cursos (certificados imprimibles)
- 📱 Comunicados Internos (tablón con prioridades)

### Mejoras de UI
- Sidebar plano con secciones y acceso directo a todos los módulos
- Comunicados críticos aparecen en el Dashboard
- 6 botones de acceso rápido a módulos extra en Dashboard

## v4.0
- Asistencia completa + historial + reportes mensuales
- Directorio visual de empleados
- Organigrama por departamento
- Importación masiva de empleados por CSV
- Gestión de usuarios y roles
- Historial de planillas
- Backup JSON + restauración

## v3.0
- Planilla completa (CSS/SE/13er mes)
- Recibo de pago individual imprimible
- Horas extras (Regular/Nocturna/Festivo)
- Deducciones personalizadas
- Liquidación con carta imprimible
- Calendario de RRHH (cumpleaños, vacaciones, contratos)
- Centro de alertas (contratos, permisos, vacaciones)
- SQL Schema para Supabase (21 tablas + RLS)

## v2.0
- Motor de plantillas de documentos
- Generación de PDF (contratos, cartas, certificaciones)
- Historial de documentos generados
- Módulo completo de vacaciones y licencias
- Calculadora de vacaciones (Art. 54 C. Laboral Panamá)
- Reportes con gráficos + exportación CSV

## v1.0
- Login JWT + Registro multiempresa
- CRUD completo de empleados (20+ campos)
- Dashboard con estadísticas y gráficos
- Modo Demo con datos precargados
- Modo claro/oscuro
- Soporte ES/EN
