# 🏢 HR Admin Panel v5.1 — Panamá

Sistema completo de gestión de Recursos Humanos construido con **AdminLTE 3 + JavaScript + Supabase**.

## 🚀 Inicio Rápido

### Opción A — Modo Demo (sin base de datos)
1. Abre `hr_admin_panel.html` en tu navegador
2. Haz clic en **"Modo Demo"**
3. Usuario: `admin@demo.com` · Contraseña: cualquiera (4+ caracteres)

### Opción B — Con Supabase (producción)
1. Crea un proyecto en [supabase.com](https://supabase.com)
2. Ejecuta `schema.sql` en **Supabase → SQL Editor**
3. Abre `hr_admin_panel.html` → ingresa tu URL y Anon Key
4. Registra tu empresa y comienza

## 📋 Módulos Incluidos

| Módulo | Descripción |
|--------|-------------|
| **Empleados** | CRUD completo, ficha, directorio, organigrama |
| **Asistencia** | Registro diario, historial, reportes |
| **Vacaciones** | Saldos, solicitudes, aprobación (Art. 54 C. Laboral) |
| **Incapacidades** | Control médico CSS Panamá (70% del día 4) |
| **Reclutamiento** | Vacantes, candidatos, pipeline de selección, Ley 1/1986 |
| **Planilla** | CSS 9.75%, SE 1.25%, ISR, aportes patronales, Décimo 3er Mes |
| **Horas Extras** | Regular +25%, Nocturna +50%, Festivo +75% |
| **Deducciones** | Préstamos, embargos, caja de ahorro |
| **Liquidación** | Arts. 225-230 C. Trabajo · retenciones legales por componente |
| **Plantillas** | Contratos, cartas, certificaciones, PDF |
| **Evaluaciones** | Desempeño por criterios con categorías |
| **Capacitaciones** | Cursos, inscripciones, certificados |
| **Encuestas** | Clima laboral con resultados estadísticos |
| **Uniformes** | Control de uniformes y equipos asignados |
| **Comunicados** | Tablón interno con prioridades |
| **Calendario** | Cumpleaños, vacaciones, contratos |
| **Reportes** | 6 tipos de reporte + gráficos + CSV |
| **Alertas** | Contratos, permisos, vacaciones acumuladas |
| **SQL Schema** | 24 tablas con RLS multiempresa |
| **Backup** | Exportar/importar JSON + CSV |

## 🔐 Roles
- **Admin** — Acceso total
- **RRHH** — Gestión de personal
- **Supervisor** — Aprobaciones
- **Empleado** — Solo lectura propia

## ⚖️ Leyes Aplicadas (Panamá)
- **Art. 54 C. Laboral** — Vacaciones (1 día/11 trabajados)
- **Arts. 212-213** — Preaviso: 1 sem / 2 sem / 1 mes / 2 meses según antigüedad
- **Arts. 225-230** — Liquidación e indemnización
- **Ley 51/2005** — CSS: 9.75% empleado / 12.25% patronal / 7.25% sobre Décimo 3er Mes
- **Código Fiscal** — ISR: 0% hasta B/.11k / 15% hasta B/.50k / 25% sobre el exceso
- **Ley 1/1986** — Reclutamiento: regla del 90% empleados panameños
- **C. Laboral** — Horas extras: +25% / +50% / +75%
- **CSS** — Incapacidades: 3 días empleador, resto 70% CSS

## 🛠 Tecnologías
- AdminLTE 3.2 + Bootstrap 4.6
- JavaScript ES2017 (async/await)
- Chart.js 3.9
- Supabase (PostgreSQL + Auth + RLS)

## 🔗 Repositorio
[github.com/livant05/hr-admin-panel-v5](https://github.com/livant05/hr-admin-panel-v5)

## 📞 Soporte
Generado con Claude (Anthropic) · 2026
