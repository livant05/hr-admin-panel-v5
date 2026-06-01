# Changelog — HR Admin Panel

**Repositorio:** [github.com/livant05/hr-admin-panel-v5](https://github.com/livant05/hr-admin-panel-v5)


## v5.1 (05/31/2026) — verificado en browser
### Comprobante de Pago
- ⚙️ Toggles para personalizar el comprobante antes de imprimir
  - **Horas Extras (HH.EE.)**: activa/desactiva el desglose de overtime y sus retenciones (CSS, SE, ISR)
  - **Aportes Patronales Referenciales**: activa/desactiva la sección de aportes del empleador
  - Ambos desactivados por defecto — recibo limpio con solo salario base y deducciones legales

### Dashboard
- 🖥️ Rediseño moderno del Dashboard
  - Tarjetas de métricas: reemplazan el estilo AdminLTE `small-box` por cards planas con acento lateral de color, ícono redondeado y número grande
  - Hover con elevación suave en tarjetas de métricas
  - Acciones Rápidas: grilla de íconos 3×2 con animación al hover — reemplaza los botones de contorno
  - "Directorio" reemplazado por "Reclutamiento" en Acciones Rápidas

### Configuración
- 🗑️ Eliminado el selector de idioma (ES/EN) — la app opera exclusivamente en español; el código `setLang` era dead code
- 🏢 Administración de Sucursales: catálogo con CRUD completo (agregar / eliminar)
  - ✏️ Edición inline del nombre de la sucursal
- 🗂️ Administración de Departamentos: edición inline del nombre
- 💼 Administración de Cargos: edición inline del nombre
- 👥 Administración de Usuarios: botones ✏️ editar y 🗑️ eliminar por fila — modal pre-llena nombre, email y rol; título cambia entre "Nuevo Usuario" / "Editar Usuario"
- 🛡️ Administración de Roles: catálogo con CRUD completo — alimenta dinámicamente el selector de rol en gestión de usuarios
  - ✏️ Edición inline del nombre del rol
  - 🔑 Permisos por rol: modal con 12 módulos configurables via toggles (Dashboard, Empleados, Asistencia, Vacaciones, Planilla, Horas Extras, Deducciones, Liquidación, Documentos, Reportes, Reclutamiento, Configuración)
- 👤 Tab Laboral del empleado: campo Sucursal cambiado de texto libre a `<select>` poblado desde el catálogo de sucursales

### Verificación en Browser
- ✅ Selector de idioma eliminado — `setLang` confirmado `undefined` en runtime
- ✅ Sucursales: listado, edición inline y agregar/eliminar verificados
- ✅ Roles: listado, edición inline, modal de permisos con 27 checkboxes (11 toggles + 16 CRUD) verificados
- ✅ Tab Laboral: `<select>` de Sucursal poblado dinámicamente — refleja sucursales agregadas en runtime
- 0 errores de consola

### Dark Mode
- 🌙 Dark mode consistente con el look Tailwind — todos los componentes del área de contenido respetan el tema oscuro
  - Cards: navy `#0f2744`, headers `#0a1f38`, bordes `#1e3a5f`
  - Tablas: headers y filas en paleta navy, texto `#cbd5e1`
  - Tabs: underline azul claro (`#60a5fa`) en modo oscuro
  - Inputs y selects: fondo oscuro, ícono de dropdown adaptado
  - Modales, paginación y alertas consistentes
  - Fondo general del contenido: `#0d1b2e`

### UI / Diseño Global
- ✨ Look Tailwind CSS reproducido con CSS custom en todas las secciones (sin mezclar frameworks)
  - Cards: esquinas redondeadas (12px), sombra sutil, borde `slate-200`
  - Tablas: headers uppercase en gris, espaciado refinado, separadores suaves
  - Badges: estilo pill (`border-radius: 9999px`)
  - Inputs y selects: bordes redondeados, focus ring azul corporativo
  - Tabs: estilo underline activo, sin caja
  - Fondo de contenido: `#f8fafc` (slate-50)
  - Sidebar sin cambios

### Diseño
- 🎨 Paleta de colores corporativos aplicada (sidebar sin cambios)
  - Navy `#1e3a5f` — Total Empleados, card headers info
  - Teal `#0d7490` — Activos, card headers success
  - Slate Blue `#2563a8` — Presentes Hoy, card headers warning, botones primarios
  - Borgoña `#8b1c1c` — Alertas Activas, card headers danger
  - Acero `#334155` — Cumpleaños del Mes
  - Verde corporativo `#15803d` — badges de éxito
  - Gráficos Chart.js actualizados con la misma paleta

### Módulo Empleados
- 👥 Tabla de empleados con vistas por tabs — mismos filtros y paginación compartidos
  - **Personales**: Cédula, N° SS, Fecha Nac., Teléfono, Sexo, Nacionalidad
  - **Laborales**: Cargo, Departamento, Ingreso, Salario, Sal/Hora, Tipo Contrato, Vence
  - **Emergencia**: Contacto de Emergencia, Teléfono, Parentesco

### Correcciones — Segunda Auditoría
- 🔴 Print Planilla: columna ISR faltaba en el encabezado impreso — todas las columnas quedaban corridas (C1)
- 🐛 CSV export: `slice(0,12)` corregido a `slice(0,13)` — la columna "Costo" ya no se pierde (W1)
- 🐛 Schema SQL: columna `total_isr NUMERIC` agregada a `payroll_history` — antes se intentaba guardar en campo inexistente (W2)
- 🐛 Logout: `doLogout()` ahora resetea los 23 arrays `DS` — datos demo ya no se filtran entre sesiones (W3)
- 🐛 Liquidación: días de vacaciones se auto-llenan desde `leave_balances.remaining_days` al seleccionar empleado (W4)
- 🐛 Recibo de pago: `netFinal` ahora respeta el toggle HH.EE. — el recibo cuadra cuando el toggle está desactivado (W5)
- 🐛 Dashboard: "Costo Empresa" ahora incluye CSS patronal 7.25% sobre Décimo (`decCSSP`) (W6)
- 🐛 Perfil de empleado: mini-planilla muestra fila ISR — Bruto − CSS − SE − ISR = Neto mostrado (W7)

### Correcciones (Auditoría)
- 🐛 Planilla: `prCalc` ahora incluye CSS patronal 7.25% sobre provisión del Décimo 3er Mes — costo empresa estaba subestimado (W2)
- 🐛 Liquidación: fórmula de Preaviso corregida según Art. 212 C. Trabajo Panamá (W3)
  - < 6 meses → 1 semana | 6m–1 año → 2 semanas | 1–5 años → 1 mes | + 5 años → 2 meses
  - Solo aplica en: Despido Injustificado, Mutuo Acuerdo, Jubilación
  - NO aplica en: Renuncia Voluntaria, Despido Justificado, Vencimiento de Contrato
- 🐛 Recibo de pago: Horas Extras ahora retiene CSS 9.75% + SE 1.25% + ISR — antes se sumaban brutas al neto (error de cumplimiento fiscal) (W1)
- 🐛 Recibo de pago: deducciones de salario y HH.EE. unificadas en una sola fila por concepto (CSS, SE, ISR combinados)
- 🐛 Renombrado "13er Mes" → "Décimo 3er Mes" / "Décimo" en todos los módulos: tabla de planilla, recibo de pago, export CSV, impresión de planilla (M1)
- 🐛 CSV export de planilla: columna ISR agregada al encabezado (faltaba tras el cambio anterior)
- 📄 README actualizado a v5.1 — módulo Reclutamiento, ISR, leyes actualizadas, nombres de archivo correctos (M2)
- 🗑️ Directorio `js/` eliminado — código muerto que nunca fue importado por la app (M3)
- 🐛 Liquidación: prefijo `B/.` duplicado en el label de Vacaciones Proporcionales corregido
- 🐛 ISR: base anual corregida a `salario × 13` según método DGI Panamá (12 meses + Décimo 3er Mes) — aplica en Planilla, Recibo de Pago y Liquidación
  - Ejemplo B/. 3,200/mes: ISR anterior B/. 342.50 → correcto B/. 382.50

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
