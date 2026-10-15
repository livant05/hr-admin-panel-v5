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

`migrations/0003_seed.sql` seeds the existing company and its admin user. The
email and the password hash are in that file; the password is a public
development default, so treat this login as local-only:

- change it right after the first login on any database that is not a
  throwaway local copy;
- with `APP_ENV=production` the API refuses to start while any user still has
  the seed password (`cmd/api/seed_guard.go`, see `docs/cutover-runbook.md`).

```sh
# SEED_EMAIL / SEED_PASSWORD: the seeded admin from migrations/0003_seed.sql
curl -X POST http://localhost:8100/api/auth/login \
  -H 'Content-Type: application/json' \
  -d "{\"email\":\"$SEED_EMAIL\",\"password\":\"$SEED_PASSWORD\"}"
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
`POST /api/auth/login` and `POST /api/auth/register`; the device ingestion endpoints join it in a later slice.

**Login counts only failures.** A successful login does not consume budget, so users behind a
shared NAT are not locked out by each other's valid sessions. Any 401 outcome (wrong password,
unknown email, inactive user, empty stored hash) consumes one token. A request whose client key has
no token left is rejected with 429 before any bcrypt work, even if its password is correct. Register
shares the same strict bucket but charges every request, success included, and never refunds: it has no
legitimate success stream and each success creates a tenant. Device ingestion also charges every request.

**Rate limiting** is an in-process token bucket per client IP (burst equals the per-minute rate),
so each backend instance keeps its own counters. At most 10,000 client keys are tracked: beyond
that the least recently used one is evicted in constant time (no sweep), which forgets its debt. An
attacker flooding more distinct keys than the cap could therefore reset a victim's budget; /64
keying for IPv6 and the cap size keep that expensive. Settings:

| Variable | Default | Meaning |
|---|---|---|
| `AUTH_RATE_PER_MIN` | `10` | Failed login attempts and every register request, per client IP per minute (one shared bucket). |
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

## Public register (`POST /api/auth/register`)

Public (no token), behind the shared `AuthLimiter` described above. Body `{company_name, first_name,
last_name?, email, password}`; unknown keys (`role`, `status`, `company_id`, ...) and bodies over
4 KiB are a `400 bad_request`. Validation failures are `400 validation_failed` with `fields`:
`company_name` 1-200 characters (trimmed), `first_name` 1-100, `last_name` at most 100 (none of the
three may contain control characters such as NUL or a newline), `email` a
plausible address of at most 254 characters, `password` 8-72 **bytes** (`auth.ValidatePassword`; a
37-character string of 2-byte runes is too long).

The email is trimmed and lowercased. The password is hashed before the transaction opens; one
transaction then creates the company and its first user (always `admin`, `active`). A duplicate email
in any case or company hits the unique `lower(email)` index, rolls the transaction back (no company
remains) and answers `409 conflict`. On success the answer is `201` with exactly the login payload
(`access_token`, `token_type`, `expires_in`, `company_name`, `user` with `user_metadata`), so the
caller is logged in; it never contains `password_hash`. The login view's "Registrar nueva empresa" form calls it and logs the new admin in.

Known limitation (accepted 2026-10-14): `409` vs `201` tells an unauthenticated caller whether an email
already belongs to a user in any company. The shared `AuthLimiter` bounds how fast that can be probed;
hiding it would need email verification (same answer either way, confirmation mail, no auto-login),
which the API does not have.

## Users and roles (admin only)

`/api/users` (list, get by id, create, patch, delete) and the write routes of `/api/roles` (create, update, delete)
require the JWT role `admin`; any other role gets `403 forbidden` before the body or the database
is touched. Role reads stay open to every authenticated role.

`users.role` holds the role **name**, and permissions are looked up by it. So:
- `PATCH /api/roles/{id}` that changes `name` renames, in the same transaction, every user of the
  company that held the old name; they keep the role's permissions. A permissions-only PATCH
  touches no user.
- `DELETE /api/roles/{id}` of a role some user still holds is `409 conflict` with the message
  "N usuario(s) tienen este rol", and nothing is deleted. Reassign those users first.
- `admin` (any case, trimmed) is reserved: creating or renaming a role to it is `400
  validation_failed` with `fields.name` = `reserved name`, because every permission check bypasses
  on that name. A legacy row already named `admin` (imported data) cannot be renamed either: the
  rename would carry every administrator along. Its permissions stay editable.

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

### Sessions and revocation

Every authenticated request is checked against the database, not only against the JWT signature.
The auth middleware (`auth.Signer.Middleware`) takes a `SessionLookup` (wired in `Routes` to one
primary-key query on `users`: status, role, company, `token_version`) and answers `401` unless the
user exists, is `active` and the token's `tv` claim equals `users.token_version`. `role` and
`company_id` in the request context come from that row, never from the token, so a demotion applies
on the next request. A lookup failure is a `500` (fail closed). Tokens issued before migration
`0013` have no `tv` claim and count as version 0; they stay valid until they expire or the user's
version is first bumped.

The version is bumped (every token of that user, on every device, stops working):

- `POST /api/auth/logout` (authenticated, any role, `204`) bumps the caller's own version.
- `PATCH /api/users/{id}` that actually changes `role` or `status`, and `DELETE /api/users/{id}`
  that deactivates, bump the target's version in the same transaction. Renames and unchanged values
  do not. The affected user must sign in again.

Migration `0013_users_token_version.sql` must be applied **before** the new binary starts (the
middleware reads the column on every request).

## Module permissions (server-side)

The frontend hides modules a role cannot use; the API enforces the same keys from `roles.permissions`
(`routes.go`, `protectedRoutes`). The JWT role `admin` always passes. Any other role needs a truthy
module key (a module whose sub-keys are all off is denied); a missing role row or key is
`403 forbidden`, fail-closed. The gate runs before the handler, so a denied caller learns nothing
from validation errors. Every protected route carries a classification and
`TestRoutes_EveryProtectedRouteIsClassified` fails when a route is added without one.

| Resource | Read | Write |
|---|---|---|
| `me`, `companies`, `roles` | any authenticated | `roles` writes: admin (in handler) |
| `departments`, `positions`, `branches` | any authenticated | `settings` |
| `users`, `zkteco_devices` claim/unclaimed, `zkteco_unknown_pins` | admin (in handler) | admin (in handler) |
| `zkteco_devices` list / rename | any authenticated | any authenticated |
| `employees` | any authenticated, sensitive fields redacted (below) | `employees` |
| `evaluations`, `uniforms` | `employees` | `employees` |
| `attendance_logs` | `attendance` | `attendance` |
| `leave_balances`, `leave_requests` | `vacations` | `vacations` |
| `medical_records` | `medical` (in handler) | `medical` (in handler) |
| `overtime_logs` | `overtime` | `overtime` |
| `deductions` | `deductions` | `deductions` |
| `payroll_history` (incl. `calculate`) | `payroll` | `payroll` |
| `liquidation_history` (incl. `calculate`) | `liquidation` | `liquidation` |
| `employee_pay_records` (incl. `bases`) | `payroll` or `liquidation` | `liquidation` |
| `document_templates`, `generated_documents` | `documents` | `documents` |
| `job_postings`, `candidates`, `applications` | `recruitment` | `recruitment` |

**Employees redaction.** `GET /api/employees` and `GET /api/employees/{id}` stay open so the
directory and birthday calendar keep working, but for a role holding neither `employees` nor
`payroll` the response returns JSON `null` for `salary`, `hourly_rate`, `cedula`, `ss_number`,
`address`, `emergency_contact`, `emergency_phone` and `emergency_relation`. `phone` and
`birth_date` stay visible. Admin and permitted roles see every field.

Cross-module widgets (dashboard attendance today, payroll summary, notifications) come back empty
for roles without that module; the frontend already treats a failed load as empty.

## Companies (own company, read only)

`GET /api/companies` returns a one-element array with the caller's own company and
`GET /api/companies/{id}` returns it by id; any authenticated role may read. Another company's id
is `404` on the by-id route and `[]` as a list filter. There are no write routes.

## Recruitment: job postings

`/api/job_postings` is tenant-scoped and requires the `recruitment` permission. `GET` lists (filters `id`,
`status`; `_limit`/`_offset`; any other parameter, including `_order`, an empty or unknown `status` is `400 validation_failed`),
`GET /{id}` returns one posting (another company's id is `404`), and `POST` creates one and returns a
one-element array. Body fields: `title` (required, trimmed, at most 200 characters), `department`,
`location`, `description`, `requirements`, `salary_min`, `salary_max` (null or a number from 0 to
99999999.99, max not below min) and `status` (`open` default, `paused`, `closed`). Unknown fields,
including `id`, `company_id` and `created_at`, are `400 bad_request`; invalid values are
`400 validation_failed` with `fields`.
A blank optional string is stored as `""` (not `NULL`; only an omitted or `null` field is `NULL`), and a
request body over 1 MiB is `400 bad_request`.

`PATCH /{id}` is a partial update returning a one-element array: an absent field is kept, an explicit
`null` clears an optional field (`title` and `status` cannot be null), and the result is validated as the
whole posting, so the salary ordering also holds against a stored salary the body did not repeat.
PATCH keys are exact (here and on candidates, applications and `zkteco_devices`): `"Title"` is a
`400 bad_request`, never accepted and silently ignored.
`DELETE /{id}` returns `204`, or `409 conflict` (message carries the count) while any application still
references the posting: the schema's `ON DELETE CASCADE` never fires through the API. The posting row is
locked (`FOR UPDATE`) during the check, so an application being created concurrently is waited for
and counted. `PATCH` only takes `FOR NO KEY UPDATE`: it still serializes with other PATCHes and with
DELETE, but it never blocks an application insert.

## Recruitment: candidates

`/api/candidates` follows the same rules as job postings: tenant-scoped, requires the `recruitment`
permission, `GET` lists (filter `id`; `_limit`/`_offset`; any other parameter is `400 validation_failed`),
`GET /{id}` returns one candidate (another company's id is `404`) and `POST` creates one and returns a
one-element array. Body fields: `full_name` (required, trimmed, at most 200 characters), `email`,
`phone`, `nationality` (absent or `null` stores the default `Panameña`; a blank string is kept as
`""`), `id_number` and `resume_notes`. Unknown fields, including `id`, `company_id` and `created_at`,
and bodies over 1 MiB are `400 bad_request`; an invalid `full_name` is `400 validation_failed` with
`fields`. A blank optional string is stored as `""`, not `NULL`.

`PATCH /{id}` is a partial update returning a one-element array: an absent field is kept, an explicit
`null` clears an optional field (`full_name` cannot be null; a `null` nationality is stored as `NULL`,
the default only applies on create) and the result is validated as the whole candidate. `DELETE /{id}`
returns `204`, or `409 conflict` (message carries the count) while any application still references the
candidate, the same rule and the same locks as job postings: `DELETE` takes `FOR UPDATE` on the row and
counts, `PATCH` takes `FOR NO KEY UPDATE`, so a concurrent application insert is waited for by a delete
but never blocked by a patch.

## Recruitment: applications

`/api/applications` is tenant-scoped and requires the `recruitment` permission. `GET` lists (filters `id`,
`job_posting_id`, `candidate_id`, `stage`; `_limit`/`_offset`; any other parameter, a malformed uuid or
an empty or unknown stage is `400 validation_failed` with `fields.stage`), `GET /{id}` returns one application (another company's id
is `404`) and `POST` creates one and returns a one-element array. Body fields: `job_posting_id` and
`candidate_id` (required uuids), `stage` (`applied` default, `screening`, `interview`, `offer`, `hired`,
`rejected`), `notes` and `applied_at` (`YYYY-MM-DD`, today by default). `job_title` and `candidate_name`
are accepted because the frontend sends them but are ignored: the server copies them from the posting's
title and the candidate's name when the application is created. Unknown fields, including `id`,
`company_id` and `created_at`, and bodies over 1 MiB are `400 bad_request`.

The posting and the candidate must belong to the caller's company. The check is the insert itself
(`INSERT ... SELECT` from both rows, scoped by company): a foreign or nonexistent id gives the same
`404 not_found`, and nothing is written. The insert takes `FOR KEY SHARE` on both rows up front, so a
guarded delete of either (`FOR UPDATE`) waits for it and then answers `409`, and an insert that loses to
a delete answers `404` instead of a foreign key error.

A candidate has at most one application per posting (unique index from migration `0015`): a second
`POST` for the same pair, such as a retry after a timeout, or a `PATCH` that re-points onto an existing
pair is `409 conflict` with the message "El candidato ya tiene una postulación para esta vacante", and
nothing is written. Imported rows whose posting or candidate is gone (`NULL`) never collide.

`PATCH /{id}` is a partial update returning a one-element array: an absent field is kept, `notes` may be
`null` to clear it, and `job_posting_id`, `candidate_id`, `stage` and `applied_at` cannot be null
(`400 validation_failed`). A stage change is only a stage change: moving an application to `hired` creates
no employee. Re-pointing `job_posting_id` or `candidate_id` to another company's row, or to one that does
not exist, is `404` and the application is unchanged; a re-point re-derives `job_title` or
`candidate_name` from the new row, while an id sent unchanged (the frontend resends both) keeps the
stored names. The new target is locked `FOR KEY SHARE` for the rest of the transaction, so a guarded
delete of it waits for the PATCH and then answers `409`. An application whose foreign key is `NULL`
(imported data) can still change stage. `DELETE /{id}` is a hard delete (`204`; another company's id is
`404`): nothing references an application.

## ZKTeco devices and unknown PINs

`GET /api/zkteco_devices` lists the caller's devices (any authenticated role; filter `id`;
`_limit`/`_offset`; any other parameter, including `_order`, is `400 validation_failed`), newest
`last_seen` first and never-seen devices last. Devices with no company (the unclaimed pool) never
appear here. `PATCH /api/zkteco_devices/{id}` (any authenticated role) takes `name` and `location`
only and returns a one-element array: an absent key is kept, `null` clears, a present value is trimmed
and must be 1 to 200 characters (`400 validation_failed` with `fields`). Any other key, such as
`serial_number`, `company_id` or `status`, is `400 bad_request` and nothing changes. A device of another
company, an unclaimed device and a missing id are all `404`. There is no `GET /{id}`; read one device
with the `id` filter.

`GET /api/zkteco_devices/unclaimed` (admin only, `403` otherwise) lists the pool of devices that
heartbeated without a company, so an admin can recognize the serial number before claiming. It takes
`_limit`/`_offset` only. `POST /api/zkteco_devices/{id}/claim` (admin only) assigns an unclaimed device
to the caller's company and returns a one-element array. The company always comes from the token: the
body is optional and must be an empty object, so a `company_id` (or any key), `null` or a non-object is `400 bad_request`; the body is capped at 1 KiB (a larger one is also `400 bad_request`). The
claim is one `UPDATE ... WHERE id = $2 AND company_id IS NULL`, so two concurrent claims serialize on
the row and exactly one gets `200`. A device that does not exist, belongs to another company or is
already yours all answer `404` and the response never says who owns it.
A successful claim also deletes, in the same transaction, the ingestion rejection counters and unknown PINs
that earlier punches left under that serial number, so the new owner never inherits data collected before
the claim.

`GET /api/zkteco_unknown_pins` is admin only (`403` otherwise, checked before any query validation).
The table has no `company_id`, because an unresolved PIN has no employee and therefore no company, so
tenancy comes from the device that punched it: the list joins `zkteco_devices` on `serial_number` and
returns only pins of devices claimed by the caller's company (pins of other companies' devices and of
unclaimed devices are never returned). Since migration `0012` a pin is unique per `(serial_number, pin)`,
so the same PIN on two companies' devices is two rows. Filter `resolved` (`true` or `false`; an empty or other value is
`400 validation_failed`), `_limit`/`_offset`, newest `last_seen` first. It is read-only.

## ZKTeco ADMS device endpoints (PROVISIONAL, gated by OPS-1)

> **Provisional.** The paths, response bodies, option text, the `/iclock/devicecmd` route and the
> 600 requests/minute limit mirror the retired Supabase Edge Function and have not been checked against a
> real device. They are gated by OPS-1 (real-device check): do not repoint production devices to the Go
> server before it. What is binding is the behavior below, not the wire format.

Devices push to `http://<host>/iclock/...` with no JWT. The routes live on the outer mux, outside `/api/`,
so they do not change the auth of any `/api/*` route. nginx routes `/iclock/` to the backend
(`client_max_body_size 2m`).

| Request | Reply |
|---|---|
| `GET /iclock/cdata?SN=<serial>&options=all` | `200` option text (`GET OPTION ...`); heartbeat |
| `GET /iclock/getrequest?SN=<serial>` | `200 OK`; heartbeat (no command is ever pending) |
| `POST /iclock/cdata?SN=<serial>&table=ATTLOG` | `200 OK: <n>` with `n` = punches recorded |
| `POST /iclock/cdata?SN=<serial>&table=<anything else>` | `200 OK`, nothing processed |
| `POST /iclock/devicecmd?SN=<serial>` | `200 OK`, nothing written |

- `SN` (or `sn`) must match `^[A-Za-z0-9_-]{1,64}$`, otherwise `400` and nothing is written. An unknown
  serial self-registers as an unclaimed device (`company_id` NULL, online); a heartbeat never changes
  `company_id`. An admin claims the device (`POST /api/zkteco_devices/{id}/claim`).
- Attendance is written only for **claimed** devices, and the PIN is resolved against the employees of the
  device's company only. A punch from an unclaimed device is counted `device_unclaimed` and answered
  `OK: 0`, so the device does not retry it forever.
- Check-in (codes 0/4) keeps the earliest time and never clears `time_out`; check-out (1/5) keeps the
  latest time and needs an existing check-in (`no_checkin` otherwise). Check-ins of a batch are applied
  before its check-outs, so line order does not matter, and re-pushing a batch is idempotent.
- Rejected punches are counted per `(serial_number, reason)` in `zkteco_ingest_rejections` (operator SQL
  only, no API yet). Reasons: `device_unclaimed`, `not_found`, `ambiguous`, `no_checkin`, `malformed`,
  `unsupported_code`. A cedula that exists only in another company is stored exactly like `not_found`
  (counter and unknown PIN), so nothing tenant-visible reveals that it exists elsewhere; only the
  `adms_batch` server log line carries a `tenant_mismatch` count, never a PIN or cedula. This deviates
  from the original spec text, which stored `tenant_mismatch` (decision CO-S5a-1).
- Unresolved PINs are upserted per `(serial_number, pin)` with `attempts + 1`.
- Limits (error replies use the JSON error envelope; devices read only the status): body over 2 MiB or
  more than 10,000 non-empty lines is `413` with no writes at all (not even the heartbeat); the per-client
  rate limit (`ADMS_RATE_PER_MIN`, default 600, keyed on the client IP behind a trusted proxy) answers `429`
  with `Retry-After`. Devices behind one customer NAT share one bucket.
- Every database write of one POST happens in one transaction; a database error rolls the whole request
  back and answers `500`, so the device retries and idempotency makes the retry safe.
- Existing volumes do not run new migrations: apply every migration the volume is missing (not only
  `0011` and `0012`), in order, before using these endpoints. `UpsertUnknownPin` needs the constraint
  from `0012` and fails the whole batch without it. See "Cutover and production" below.
- **Go-live blockers (OPS-1):** the ATTLOG field order (the parser reads index 3, which published
  documentation lists as the verify mode, not the in/out status), device authentication (devices are
  identified only by serial number) and unclaimed-pool flooding are open until checked on a real device.
  Details and sources: [`docs/cutover-runbook.md`](../docs/cutover-runbook.md).

## Attendance: created_after and ordering

`GET /api/attendance_logs` also takes `created_after=<RFC 3339 timestamp>` and returns only rows whose
`created_at` is strictly greater (tenant-scoped, combinable with the other filters and `_limit`/`_offset`).
A malformed or empty value is `400 validation_failed` with `fields.created_after`. Encode a `+hh:mm`
offset as `%2B` (or use `Z`), otherwise the `+` reaches the server as a space. This is the only endpoint
that accepts `_order`, and only `created_at.desc` (newest insert first) and `created_at.asc` (oldest
first). The `id` breaks ties in the same direction as `created_at` (`created_at DESC, id DESC` and
`created_at ASC, id ASC`), so `_limit`/`_offset` paging is deterministic and returns each row exactly
once; any other value is `400 validation_failed`, and every other endpoint still rejects `_order`.
Without `_order` the list keeps its `date DESC` order.

The ZKTeco live feed (TT-1) is a cursor over this list. A strict `created_at > cutoff` can miss a row
that commits late with an older `created_at`, so the client keeps `maxSeenCreatedAt` and polls with
`created_after = maxSeenCreatedAt - OVERLAP` (the panel uses 120 s), `_order=created_at.asc` and
`_limit=200`, pages with `_offset` until a page returns fewer rows than the limit, and drops rows it has
already shown by `id` (a bounded seen-set). The first call, which only learns the starting point and
shows nothing, is `_order=created_at.desc&_limit=1`.

## Dev procedure

The login screen talks to this backend, so there is nothing to seed by hand.

1. Start the stack: `docker compose up -d --build`.
2. Open `http://localhost:8100` and log in normally. With the local dev seed that is
   the seeded admin (see "Seeded login"), or use "Registrar nueva empresa".
3. The token lasts 24 hours; after that, or on any 401, the app returns to the login view. To leave
   earlier use "Cerrar sesión", which calls `POST /api/auth/logout` (best effort: the browser session
   is cleared even if the call fails) and invalidates every token of that user, or clear `hr_sess`
   and `hr_meta` from `localStorage`.
4. After a `git checkout`, `git pull` or any change to `nginx/default.conf`, run
   `docker compose restart nginx`. The page and the nginx config are single-file bind mounts: git
   replaces the file with a new inode, the container keeps the old one and silently serves the
   previous version (`docker compose up` does not recreate an unchanged container). Check with
   `curl -s localhost:8100/ | cmp - ../hr_admin_panel.html`.

**Dev override of the API base URL.** By default the page calls `/api/...` on its own origin (nginx
proxies it). To point the page at a different backend, set `localStorage.hr_go_url` and reload. The
backend sends no CORS headers, so a different origin only works behind a dev proxy that adds them:

```js
localStorage.setItem('hr_go_url', 'http://localhost:8080'); location.reload();
// back to the same origin:
localStorage.removeItem('hr_go_url'); location.reload();
```

Demo mode ("Modo Demo" on the login view) never calls the API.

### Manual checklist: Users screen (Configuracion > Usuarios)

Run after logging in as the seeded admin (see "Dev procedure"). There is no JS test harness, so this checklist is the
test for the frontend slice.

- [ ] Real mode, list: the tab loads the company's users from `GET /api/users`; names, emails, role
      and Activo/Inactivo badges match the database; no row comes from the demo data.
- [ ] Real mode, edit: the pencil opens the modal populated with the name, email and role of that
      row; changing the name and saving updates the row after the list reloads; saving does not
      change the user's status.
- [ ] Real mode, create: Nuevo Usuario sends only first name, last name, email and role. The
      temporary password appears in its own modal exactly once, the copy button copies it, and after
      closing that modal (Cerrar button; the backdrop and Esc do not close it) the password field is empty in the DOM and cannot be
      shown again. The new user can log in with it through `POST /api/auth/login`.
- [ ] Real mode, delete: the trash button asks for confirmation, then the row stays in the list as
      Inactivo (soft delete) and that user can no longer log in.
- [ ] Real mode, errors: creating a duplicate email, or deactivating or demoting the only active
      admin, shows the server message as a red toast and changes nothing. A non-admin token gets
      "Solo administradores" instead of a list.
- [ ] Names or emails containing `<b>x</b>` or quotes render as text, never as markup.
- [ ] Demo mode (`localStorage.setItem('hr_demo','1')`, no `hr_sess`, reload and enter demo): the
      Users tab behaves exactly as before, local-only add, edit and delete, and never shows the
      temporary password modal.

### Manual checklist: Recruitment screen (Reclutamiento)

Run after logging in as the seeded admin (see "Dev procedure"), with the browser Network tab open. Written before the
code it tests (no JS harness: this checklist is the test for the frontend slice). Function names and
element ids are in `hr_admin_panel.html`.

- [ ] Real mode, list: the three tabs (Vacantes, Candidatos, Pipeline) load from
      `GET /api/job_postings`, `/api/candidates` and `/api/applications`; no row comes from the demo
      data and no id starts with `jp-`, `ca-` or `ap-`.
- [ ] Real mode, create: Nueva Vacante, Nuevo Candidato and Nueva Postulación each send one POST
      with the form fields only (no `company_id`, no `job_title`/`candidate_name`), close the modal
      and show the new row after the list reloads, with the server-generated uuid as its id.
- [ ] Real mode, edit: the pencil opens the modal populated from the server row; saving sends one
      PATCH and the list reloads with the change. Re-pointing a postulación to another vacante or
      candidate of the same company works.
- [ ] Real mode, save buttons: a double click on Guardar (`#rj-save`, `#rc-save`, `#ra-save`) sends
      exactly one request; the button is disabled during the request and enabled afterwards.
- [ ] Real mode, delete: the trash button asks for confirmation, sends `DELETE` and the row
      disappears after the reload. Deleting a vacante or a candidate that still has postulaciones is
      refused with a red Spanish toast ("N postulación(es) siguen asociadas ...") and the row stays.
- [ ] Real mode, hire: Contratar (only on rows in stage Oferta) sends `PATCH {"stage":"hired"}` and
      nothing else; the row shows Contratado after the reload and no employee is created.
- [ ] Real mode, errors: a salary maximum lower than the minimum shows "Datos inválidos: revisa
      salario máximo" and keeps the modal open; a postulación whose vacante was deleted in another tab
      shows a Spanish not-found toast; a server or network failure shows a red toast and never
      fakes a local change.
- [ ] Texts such as `<b>x</b>` or quotes in titles, names, notes and emails render as text, never as
      markup (including inside the candidate and vacante select options).
- [ ] Demo mode (`hr_demo`, no `hr_sess`): the three tabs behave exactly as before, local-only
      create, edit, delete and hire, with zero network calls.

### Manual checklist: ZKTeco screen (Configuracion > ZKTeco) and live feed

Run after logging in as the seeded admin (see "Dev procedure"), with the Network tab open. Written before the code it
tests (no JS harness for the screen; the cursor logic is covered by `node tools/zk-feed/harness.mjs`, which drives the real feed code against a fake endpoint). Seed devices and
unknown PINs with SQL or the ADMS endpoint, and check the dev database has every migration first.

- [ ] Real mode, devices: the table loads from `GET /api/zkteco_devices` (no `/rest/v1/` call), shows
      only this company's devices, and an online badge only when `last_seen` is under 5 minutes old.
- [ ] Real mode, edit: the pencil edits name and location in place; saving sends one
      `PATCH /api/zkteco_devices/{id}` with `name` and `location` only, shows "Reloj actualizado" and
      reloads. A name of 201 characters shows "Datos inválidos: revisa ..." and keeps the editor.
- [ ] Real mode, server text: the URL field shows `<location.host>/iclock/` and the setup steps no
      longer mention Supabase, Edge Functions, port 443 or the deploy card; the port and HTTPS
      guidance is labelled provisional.
- [ ] Real mode, test connection: Probar conexion sends exactly one `GET /api/health`, toasts success,
      and the device count is the same before and after (no `TEST` device is created).
- [ ] Real mode, unknown PINs: the card loads from `GET /api/zkteco_unknown_pins?resolved=false&_limit=20`
      for an admin. As a non-admin it shows "Solo administradores" instead of a table or a raw 403.
- [ ] Real mode, unclaimed devices: an admin sees the pool from `GET /api/zkteco_devices/unclaimed`
      with serial numbers; Reclamar sends one `POST /api/zkteco_devices/{id}/claim` (no body), the
      device moves into "Relojes Registrados" after the reload, and a double click sends one request.
      Claiming a device someone else claimed first shows a not-found toast and refreshes the pool.
      A non-admin sees "Solo administradores".
- [ ] Real mode, escaping: a device name, location or serial number like `<b>x</b>` or `'"` renders as
      text, and the edit and claim buttons still work for it.
- [ ] Live feed, seed: opening Asistencia sends one `GET /api/attendance_logs?_order=created_at.desc&_limit=1`
      and then a silent catch-up poll; existing rows produce no toast and the "ZKTeco en vivo" badge shows.
- [ ] Live feed, poll: every 30 seconds one `GET /api/attendance_logs?created_after=...&_order=created_at.asc&_limit=200&_offset=0`
      goes out, where `created_after` is the newest seen `created_at` minus 120 seconds. Inserting a
      punch row (SQL) toasts it once and refreshes the list; the next polls, which re-read it through
      the overlap window, do not toast it again.
- [ ] Live feed, paging: with more than 200 new rows between polls, the same poll continues with
      `_offset=200` until a page is shorter than 200 and every row is toasted at most once (the toast
      list is capped at five, followed by a "+N marcadas anteriores" toast).
- [ ] Live feed, failures: stopping the API makes polls fail silently (no toast loop), and the next
      successful poll catches up without duplicates.
- [ ] Demo mode (`hr_demo`, no `hr_sess`): the ZKTeco tab shows the same empty tables as before with
      zero network calls; Probar conexion explains that it is unavailable in demo mode.

## Migrations

Plain numbered SQL files in `migrations/`, applied automatically by Postgres's own
`docker-entrypoint-initdb.d` mechanism **only on first container creation** (a fresh volume).
To re-apply after changing a migration file: `docker compose down -v && docker compose up -d --build`
(this wipes local data — fine for dev, not for anything with real data in it).

**Idempotency rule:** every migration numbered `0010` or higher must be idempotent
(`IF NOT EXISTS`, guarded `DO` blocks, ...). The test harness (`internal/testutil`) re-applies
them on every run against an already-initialized test database; files below `0010` only run
on a fresh one.

## Cutover and production

The step-by-step Supabase cutover, rollback and end-to-end checklist live in
[`docs/cutover-runbook.md`](../docs/cutover-runbook.md). Every operational step there is marked
`[USER-OPS]`: it is yours to run, not the build's.

**Startup seed guard.** With `APP_ENV=production` the API checks, before serving, whether any
user row, active or inactive, still has the committed seed password hash (from `migrations/0003_seed.sql`).
If so it logs `seed credential present` and exits 1; a failed check also exits 1. Any other `APP_ENV`
is not checked and logs one `seed guard skipped` line naming the value. Delete the seed admin first (runbook step 6).

Environment variables for a production deployment (defaults in `env.example` and `docker-compose.yml`):

| Variable | Default | Meaning |
|---|---|---|
| `APP_ENV` | `development` | `production` turns on the seed guard above (case-insensitive). |
| `AUTH_RATE_PER_MIN` | `10` | Failed logins and register requests per client IP per minute. |
| `AUTH_ATTEMPT_RATE_PER_MIN` | `60` | All login attempts per client IP per minute. |
| `ADMS_RATE_PER_MIN` | `600` | Device ingestion per client IP per minute. Provisional until OPS-1. |
| `TRUSTED_PROXY_CIDRS` | `172.29.77.0/24` (compose) | Proxies whose `X-Real-IP` is trusted. See "Multi-tenancy". |

**Existing volumes and migrations.** Postgres runs `migrations/` only on a fresh volume. On a volume that
predates a migration, apply every missing file in order with `psql -v ON_ERROR_STOP=1 -f`, then run the
verification query from runbook step 4. After editing `nginx/default.conf`, run `nginx -s reload`
(`docker compose exec nginx nginx -s reload`): the bind-mounted file is not re-read by `docker compose up`.

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
