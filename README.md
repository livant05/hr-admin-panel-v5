# 🏢 HR Admin Panel v5.0 — Panamá

Sistema completo de gestión de Recursos Humanos construido con **AdminLTE 3 + JavaScript + Supabase**.

## 🚀 Inicio Rápido

### Opción A — Modo Demo (sin base de datos)
1. Abre `index.html` en tu navegador
2. Haz clic en **"Modo Demo"**
3. Usuario: `admin@demo.com` · Contraseña: cualquiera (4+ caracteres)

### Opción B — Con Supabase (producción)
1. Crea un proyecto en [supabase.com](https://supabase.com)
2. Ejecuta `schema.sql` en **Supabase → SQL Editor**
3. Abre `index.html` → ingresa tu URL y Anon Key
4. Registra tu empresa y comienza

## 📋 Módulos Incluidos

| Módulo | Descripción |
|--------|-------------|
| **Empleados** | CRUD completo, ficha, directorio, organigrama |
| **Asistencia** | Registro diario, historial, reportes |
| **Vacaciones** | Saldos, solicitudes, aprobación (Art. 54 C. Laboral) |
| **Incapacidades** | Control médico CSS Panamá (70% del día 4) |
| **Planilla** | CSS 9.75%, SE 1.25%, aportes patronales 13.75%, 13er mes |
| **Horas Extras** | Regular +25%, Nocturna +50%, Festivo +75% |
| **Deducciones** | Préstamos, embargos, caja de ahorro |
| **Liquidación** | Arts. 225-230 C. Trabajo Panamá |
| **Documentos** | Plantillas, contratos, cartas, PDF |
| **Evaluaciones** | Desempeño por criterios con categorías |
| **Capacitaciones** | Cursos, inscripciones, certificados |
| **Encuestas** | Clima laboral con resultados estadísticos |
| **Uniformes** | Control de uniformes y equipos asignados |
| **Comunicados** | Tablón interno con prioridades |
| **Calendario** | Cumpleaños, vacaciones, contratos |
| **Reportes** | 6 tipos de reporte + gráficos + CSV |
| **Alertas** | Contratos, permisos, vacaciones acumuladas |
| **SQL Schema** | 21 tablas con RLS multiempresa |
| **Backup** | Exportar/importar JSON + CSV |

## 🔐 Roles
- **Admin** — Acceso total
- **RRHH** — Gestión de personal
- **Supervisor** — Aprobaciones
- **Empleado** — Solo lectura propia

## ⚖️ Leyes Aplicadas (Panamá)
- **Art. 54 C. Laboral** — Vacaciones (1 día/11 trabajados)
- **Arts. 225-230** — Liquidación e indemnización
- **Ley 51/2005** — CSS: 9.75% empleado / 12.25% patronal
- **C. Laboral** — Horas extras: +25% / +50% / +75%
- **CSS** — Incapacidades: 3 días empleador, resto 70% CSS

## 🛠 Tecnologías
- AdminLTE 3.2 + Bootstrap 4.6
- JavaScript ES2017 (async/await)
- Chart.js 3.9
- Supabase (PostgreSQL + Auth + RLS)

## 📞 Soporte
Generado con Claude (Anthropic) · 2026
