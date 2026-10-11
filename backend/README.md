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

## API conventions

**Errors** use one envelope with `Content-Type: application/json`:
`{"error":{"code":"<code>","message":"..."}}` (validation errors add `"fields"`). The `code` set is
`bad_request`, `unauthorized`, `forbidden`, `not_found`, `conflict`, `validation_failed`,
`internal` and `rate_limited`.

`rate_limited` extends the original A2 set: it is returned with HTTP 429 and a `Retry-After`
header (whole seconds) when a client IP exceeds its budget on a public endpoint. Today that is
`POST /api/auth/login`; register and the device ingestion endpoints join it in later slices.

**Rate limiting** is an in-process token bucket per client IP (burst equals the per-minute rate),
so each backend instance keeps its own counters. Settings:

| Variable | Default | Meaning |
|---|---|---|
| `AUTH_RATE_PER_MIN` | `10` | Login (and register) requests per client IP per minute. |
| `ADMS_RATE_PER_MIN` | `600` | Device ingestion per client IP per minute. **Provisional** until a real-device check. |
| `TRUSTED_PROXY_CIDRS` | empty | Comma-separated CIDRs of reverse proxies whose `X-Real-IP` is believed. |

The client IP is the TCP peer address, except when the peer is inside `TRUSTED_PROXY_CIDRS` and
sends a valid `X-Real-IP` (nginx overwrites it with `$remote_addr`). `X-Forwarded-For` is never
read because clients can forge it. Leaving `TRUSTED_PROXY_CIDRS` empty behind a proxy would put
every user in the proxy's bucket; `docker-compose.yml` sets the private ranges because only nginx
reaches the backend. Never trust a range that untrusted clients can connect from directly.

## Migrations

Plain numbered SQL files in `migrations/`, applied automatically by Postgres's own
`docker-entrypoint-initdb.d` mechanism **only on first container creation** (a fresh volume).
To re-apply after changing a migration file: `docker compose down -v && docker compose up -d --build`
(this wipes local data — fine for dev, not for anything with real data in it).

**Idempotency rule:** every migration numbered `0010` or higher must be idempotent
(`IF NOT EXISTS`, guarded `DO` blocks, ...). The test harness (`internal/testutil`) re-applies
them on every run against an already-initialized test database; files below `0010` only run
on a fresh one.

## Regenerating sqlc code

Queries live in `internal/db/queries/*.sql`, config in `internal/db/sqlc.yaml`, generated
output in `internal/db/generated/` (committed, do not hand-edit). After changing a query or
the schema:

```sh
cd internal/db
go run github.com/sqlc-dev/sqlc/cmd/sqlc@latest generate
```

## Importing data from Supabase

`cmd/import` loads a JSON export of the Supabase tables into this database, keeping every
original UUID so relations (employees, evaluations, attendance...) stay intact. You export
the data yourself; the tool never talks to Supabase.

### 1. Export each table to `<table>.json`

Each file is a JSON array of row objects, exactly what PostgREST returns for
`select=*`. Put them all in one directory (for example `./export`). A missing file just skips
that table; extra files are listed and ignored.

```sh
# service_role key: keep it out of your shell history and never commit it
export SUPABASE_URL="https://<project>.supabase.co"
export SUPABASE_KEY="<service_role key>"
mkdir -p export
for t in companies departments positions branches roles users document_templates trainings \
         surveys announcements job_postings candidates payroll_history zkteco_devices \
         zkteco_unknown_pins employees attendance_logs leave_balances leave_requests \
         overtime_logs deductions employee_pay_records liquidation_history generated_documents \
         medical_records evaluations uniforms enrollments survey_responses applications; do
  curl -sS -H "apikey: $SUPABASE_KEY" -H "Authorization: Bearer $SUPABASE_KEY" \
       "$SUPABASE_URL/rest/v1/$t?select=*" -o "export/$t.json"
done
```

PostgREST caps a response at the project's `max-rows` setting (1000 by default). If a table
is larger, raise `max-rows` in the project's API settings for the export, or fetch it in pages
(`?select=*&limit=1000&offset=N`) and merge the arrays. Alternatively, run
`select json_agg(t) from <table> t;` in the SQL editor and save the result as `<table>.json`.

Supabase's own `auth.users` table is not needed: this backend owns credentials (see below).

### 2. Dry run first

```sh
cd backend
go run ./cmd/import --dir ./export --db-url "$DB_URL" --dry-run
```

`--db-url` defaults to the `DB_URL` environment variable. The dry run does all the validation
and transforms inside a transaction, prints a per-table report (rows read, would insert,
already present, invalid, dropped keys) and rolls back. It writes nothing, not even the
passwords file.

### 3. Real run

```sh
go run ./cmd/import --dir ./export --db-url "$DB_URL" --passwords-out ./import-passwords.csv
```

- The import is one transaction: any error rolls everything back.
- Exit code 0 on success, 1 on failure, 2 on bad usage.
- Invalid rows (a failed validation, a missing employee, a duplicate key...) abort the run
  and are listed with table, id and reason. Fix the export, or re-run with `--skip-invalid`
  to load the valid rows and have the skipped ones listed in the report. Rows that depend on
  a skipped row (for example attendance of a skipped employee) are skipped too.
- Keys in the JSON with no matching column (and generated columns such as
  `leave_balances.remaining_days`) are dropped and listed per table.

### Idempotency

Rows are inserted with their original `id` and `ON CONFLICT (id) DO NOTHING`. The tool never
updates: re-running the same export inserts nothing new, and rows that already exist, such
as the seeded company and admin user, are left untouched ("already present" in the report).
Because there is no update path, a row that changed in Supabase after a first import is not
refreshed.

### What is recomputed

The tool reuses the same code as the API, so imported rows satisfy the server's invariants
instead of trusting the exported values:

- `evaluations`: legacy JSON-string `scores` are unwrapped and validated (integers 1-10, all
  8 criteria, no rounding); `scores` is stored canonical, `avg` and `category` are recomputed.
  `status` defaults to `completada`.
- `medical_records`: days, employer/CSS split, `salary_basis` and `cost` come from the
  imported employee's salary. An end date before the start date, an unknown type or a span
  over 3650 days is invalid.
- `overtime_logs`: `amount` is `hourly_rate * rate(type) * hours` with the statutory rates;
  an unknown type or more than 3 hours is invalid.
- `roles`: migration `0009` is re-run, so imported roles get the `medical` permission (it
  mirrors their `attendance` access). It also applies to any other role that lacks the key.
- Duplicate `(employee_id, date)` in `attendance_logs` and `(employee_id, year)` in
  `leave_balances` within the export are invalid.
- `payroll_history`, `liquidation_history` and `employee_pay_records` are history and are
  imported as they are.

### Users and temporary passwords

Supabase password hashes are not portable, so every user that is not already in the database
gets a random temporary password (24 URL-safe characters, stored as bcrypt).
`--passwords-out <file>` is required for a real run that creates users; it writes
`email,temporary_password`, with file mode `0600`, and refuses to overwrite an existing file.
Passwords are never printed or logged. A user without an email, or whose email belongs to a
different user id, is invalid.

Handle the file as a secret: give each person their password through a secure channel, ask
them to change it at first login, and delete the file once it has been delivered. Its default
name pattern (`*passwords*.csv`) is in `.gitignore`.
