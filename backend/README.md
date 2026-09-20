# RRHH-Go

Standalone Go backend for the Ventatec HR panel (`hr-admin-panel-v5`), replacing Supabase.
Independent from every other stack on this machine — own Docker network (`rrhh_go_net`),
own Postgres, own ports (`8100` for the app via nginx, `5434` for Postgres).

## Stack

- Go 1.27, stdlib `net/http` (no router framework)
- `sqlc` + `pgx/v5` for the DB layer (no ORM)
- `golang-jwt/jwt/v5` + `bcrypt` for auth — this backend owns its own credentials now
- Docker Compose: postgres + backend + nginx (serves the static `hr_admin_panel.html`
  and reverse-proxies `/api/` to the Go backend)

## Run it

```sh
cp env.example .env   # fill in real POSTGRES_PASSWORD / JWT_SECRET for anything beyond local dev
docker compose up -d --build
```

App: http://localhost:8100/
API health: http://localhost:8100/api/health

## Seeded login

The existing real Ventatec company/user were seeded in `migrations/0003_seed.sql`:

- email: `livan.05@hotmail.com`
- password: `Admin123`

```sh
curl -X POST http://localhost:8100/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"livan.05@hotmail.com","password":"Admin123"}'
```

## Current scope — auth skeleton only

Migrations now define the full schema: 27 tables from `0001_init.sql` plus 3 more
(`employee_pay_records`, `zkteco_devices`, `zkteco_unknown_pins`) from
`0004_pay_zkteco.sql`, for 30 tables total. Only auth handlers are implemented:
`POST /api/auth/login`, `GET /api/me` (JWT-protected), `GET /api/health`.

**Not yet done** (future work, intentionally out of scope for this skeleton): CRUD for the
remaining 29 tables (employees, attendance, payroll, deductions, leave, evaluations, ZKTeco
devices, etc.) and the corresponding rewrite of every `supabase-js` call in
`hr_admin_panel.html` to call this API instead.

## Multi-tenancy

Supabase enforced company isolation via Postgres RLS reading `auth.jwt()`. That function only
exists in Supabase's managed Postgres, so it was removed from `migrations/0001_init.sql`
(see the comment there). RRHH-Go enforces isolation in `internal/auth.Middleware` instead:
it validates the JWT and injects `company_id` into the request context — every handler that
touches tenant data must read it from there, never from a client-supplied field.

## Migrations

Plain numbered SQL files in `migrations/`, applied automatically by Postgres's own
`docker-entrypoint-initdb.d` mechanism **only on first container creation** (a fresh volume).
To re-apply after changing a migration file: `docker compose down -v && docker compose up -d --build`
(this wipes local data — fine for dev, not for anything with real data in it).

## Regenerating sqlc code

Queries live in `internal/db/queries/*.sql`, config in `internal/db/sqlc.yaml`, generated
output in `internal/db/generated/` (committed, do not hand-edit). After changing a query or
the schema:

```sh
cd internal/db
go run github.com/sqlc-dev/sqlc/cmd/sqlc@latest generate
```
