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

**Login counts only failures.** A successful login does not consume budget, so users behind a
shared NAT are not locked out by each other's valid sessions. Any 401 outcome (wrong password,
unknown email, inactive user, empty stored hash) consumes one token. A request whose client key has
no token left is rejected with 429 before any bcrypt work, even if its password is correct. Other
endpoints on the same limiter (register, device ingestion) consume a token on every request.

**Rate limiting** is an in-process token bucket per client IP (burst equals the per-minute rate),
so each backend instance keeps its own counters. At most 10,000 client keys are tracked: beyond
that the least recently used one is evicted in constant time (no sweep), which forgets its debt. An
attacker flooding more distinct keys than the cap could therefore reset a victim's budget; /64
keying for IPv6 and the cap size keep that expensive. Settings:

| Variable | Default | Meaning |
|---|---|---|
| `AUTH_RATE_PER_MIN` | `10` | Failed login attempts (and, once it exists, register requests) per client IP per minute. |
| `AUTH_ATTEMPT_RATE_PER_MIN` | `60` | Looser per-client budget that counts **every** login attempt, successful ones included, so valid credentials cannot force unbounded bcrypt work. Checked before the strict budget; never refunded. |
| `ADMS_RATE_PER_MIN` | `600` | Device ingestion per client IP per minute. **Provisional** until a real-device check. |
| `TRUSTED_PROXY_CIDRS` | empty | Comma-separated CIDRs of reverse proxies whose `X-Real-IP` is believed. |

The client IP is the TCP peer address, except when the peer is inside `TRUSTED_PROXY_CIDRS` and
sends a valid `X-Real-IP` (nginx overwrites it with `$remote_addr`). `X-Forwarded-For` is never
read because clients can forge it. IPv6 clients are bucketed by their /64 prefix (IPv4 and
IPv4-mapped addresses per address), so rotating addresses inside a delegated /64 does not
yield fresh budgets. Leaving `TRUSTED_PROXY_CIDRS` empty behind a proxy would put
every user in the proxy's bucket. `docker-compose.yml` declares the `rrhh_go_net` subnet explicitly
(`172.29.77.0/24`) and trusts only that subnet, because only nginx reaches the backend on it; if
you change the subnet, change `TRUSTED_PROXY_CIDRS` with it (a test keeps them in sync). Never
trust a broad private range or one that untrusted clients can connect from directly.

Every 429 is logged at WARN as `rate limited` with `limiter` (`auth` for the strict failure
budget, `auth_attempt` for the looser attempt budget), `client` (the IP, or /64 for IPv6, that
was charged) and `retry_after_s`. Request bodies, headers and credentials are never logged. Users
behind one shared NAT can lock each other out of the strict budget (accepted behavior); these log
lines are how an operator sees it happen.

## Users and roles (admin only)

`/api/users` (list, get by id, create, patch, delete) and the write routes of `/api/roles` (create, update, delete)
require the JWT role `admin`; any other role gets `403 forbidden` before the body or the database
is touched. Role reads stay open to every authenticated role.

- `POST /api/users` takes `{email, first_name, last_name, role, status?}` and nothing else (a
  `password`, `password_hash`, `company_id` or `id` key is a 400). The email is trimmed and
  lowercased; a duplicate in any case or company is a 409. `role` is `admin`, `empleado` (the
  default) or the exact name of one of the company's roles, otherwise `validation_failed`.
- The server generates a 24-character temporary password, stores only its bcrypt hash and returns
  it once, as `temporary_password` in the 201 response (`Cache-Control: no-store`). It is never
  logged and no later call can retrieve it. Password policy elsewhere is 8-72 bytes
  (`auth.ValidatePassword`); `auth.GenerateTempPassword` is shared with `cmd/import`.
- Responses use a fixed DTO (`id, company_id, email, role, first_name, last_name, status,
  created_at`) built from queries that never select `password_hash`.
- `PATCH /api/users/{id}` takes any of `{email, first_name, last_name, role, status}` (same
  validation as create; unknown keys, including `password`, are a 400) and returns the updated row
  as a one-element array. `DELETE /api/users/{id}` is a soft delete: the row stays with
  `status: inactive`, the user can no longer log in, and repeating it is a `204` no-op. Another
  company's id is always `404`.
- **Last-active-admin guard:** a PATCH or DELETE that would leave the company with no active admin
  (demoting, deactivating or deleting the only one) is a `409 conflict` and changes nothing. The
  guard runs in one transaction that locks the company's active admins in id order, then the
  target row, so two admins demoting each other concurrently cannot both succeed.

### Residual risk: sessions outlive deactivation (AUTH-10)

A user who is deactivated (`DELETE /api/users/{id}`, `status: inactive`) or demoted keeps a working
JWT until it expires, up to 24 hours: the token carries `role` and `company_id` as signed claims
and the middleware does not re-check the user's row on each request (design D17). Login does check
`status`, so a deactivated user cannot obtain a new token. If an account is compromised, deactivation
alone does not end the current session within the day. A cache-backed per-request status check in
the auth middleware is the follow-up if this window becomes unacceptable.

## Companies (own company, read only)

`GET /api/companies` returns a one-element array with the caller's own company and
`GET /api/companies/{id}` returns it by id; any authenticated role may read. Another company's id
is `404` on the by-id route and `[]` as a list filter. There are no write routes.

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
