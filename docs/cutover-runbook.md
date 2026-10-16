# Cutover runbook: Supabase to RRHH-Go

This runbook moves production from Supabase (auth, REST, Edge Function) to the RRHH-Go
backend. It is written for the person doing the cutover, in the order the steps must happen.
Nothing here is run by the build or by an agent: **every operational step is marked
`[USER-OPS]`** and is yours to execute and sign off.

Scope: requirements SR-3, SR-4 and SR-5 of change `rrhh-go-auth-cutover`; operational items
OPS-1 to OPS-10.

## Read this first

1. **Do not start before the go-live blockers are closed.** Three of them need a real ZKTeco device and
   are still open (see [Go-live blockers](#go-live-blockers-ops-1-real-device-entry-criteria)).
   Without a device you may rehearse every other step, but you may not repoint production devices.
2. **The API refuses to start in production while the seed admin is active.** With
   `APP_ENV=production`, `cmd/api` exits 1 and logs `seed credential present` if any user row, active
   **or inactive**, has the committed seed password hash. Step 6 removes it; the guard is the backstop.
3. **Rollback stays possible for at least 14 days.** The Supabase project and the deployed Edge
   Function must stay live until the stabilization window ends (step 13).

## Quick path

| # | Step | Ops id | Gate to pass before the next step |
|---|------|--------|-----------------------------------|
| 0 | Close the go-live blockers | OPS-1, OPS-2 | Blockers B1 to B3 resolved and recorded below |
| 1 | Write freeze, Supabase export | OPS-3 | Export files exist, Supabase is read-only |
| 2 | Diff `auth.users` vs `public.users` | OPS-4 | Zero admins missing from `public.users` |
| 3 | Duplicate-email check | OPS-5 | Query returns zero rows |
| 4 | Apply every missing migration | OPS-5 | Verification query shows all `true` |
| 5 | Back up the Go database | OPS-5 | Dump file restores in a scratch database |
| 6 | Delete the seed admin | OPS-6 | Preflight query returns zero rows |
| 7 | Import: dry run, then real run | OPS-7 | Report has no invalid rows, CSV is mode 0600 |
| 8 | Distribute temporary passwords | OPS-7 | Every user has received theirs, CSV deleted |
| 9 | Claim imported devices | OPS-8 | `zkteco_devices` has no `NULL` company |
| 10 | Deploy with `APP_ENV=production` | OPS-9 | API stays up, no `seed credential present` |
| 11 | Repoint devices | OPS-8 | A real punch from a claimed device lands in attendance |
| 12 | Run the E2E checklist | OPS-9 | Every item ticked, including demo mode |
| 13 | Stabilization window, 14 days minimum | OPS-10 | Day 14 reached with no rollback |

Commands that use Docker Compose start with `cd backend` (that is where `docker-compose.yml` lives).
Replace `<host>` with the public host name devices and browsers will use.

---

## Go-live blockers (OPS-1: real-device entry criteria)

`[USER-OPS]` The ADMS wire format in `backend/internal/handlers/adms*.go` was ported from the
Edge Function and has **never been checked against a physical device**. These three items are
go-live blockers: do not execute steps 10 and 11 for devices until each is closed. They were
deferred on 2026-10-12 because no device is available yet; they stay open, not waived.

### B1. Capture a real ATTLOG line and confirm the field order

**Why it blocks.** The parser reads the in/out code from field index 3
(`attlogCodeIndex = 3` in `backend/internal/handlers/adms_parse.go`). That index was inherited from
the Edge Function (`parts[3]`, line 78 of `supabase/functions/zkteco-adms/index.ts`, now deleted from the
working tree; read it with `git show 4a741fa:supabase/functions/zkteco-adms/index.ts`). Published
ADMS documentation lists the ATTLOG fields as:

```text
PIN <TAB> time <TAB> STATUS <TAB> VERIFY <TAB> workcode ...
```

- `STATUS` (index 2): `0` check-in, `1` check-out, `2`/`3` break out/in, `4`/`5` overtime in/out.
- `VERIFY` (index 3): `1` fingerprint, `4` card, `15` face, `0` password.

If that order is right, the current code reads the **verify mode**, not the punch direction. A
fingerprint punch (`VERIFY=1`) would be stored as a check-out, a card punch (`4`) as a check-in
and a face punch (`15`) as `unsupported_code`. Nothing in the unit tests can detect this, because
they were written against the same assumption.

**What to do.**

1. Point one device at a capture endpoint (or temporarily log the raw `POST /iclock/cdata` body)
   and record 10 or more real lines covering: a check-in and a check-out by fingerprint, one by
   card or face if available, and a break or overtime punch if the device is configured for it.
2. Write the observed column order and the observed value of each field below.
3. If index 2 holds the direction: change `attlogCodeIndex` to `2`, fix the table-driven tests in
   `adms_parse_test.go` and the fixtures in `adms_test.go`, and re-run the checks. Do **not**
   change it before the capture confirms the order.
4. Decide the mapping of `2`/`3` (break) and `4`/`5` (overtime); today `4` is a check-in and `5` a
   check-out (`ING-5`, provisional).

**Check whether production data is already affected.** The Edge Function uses the same
`parts[3]`. If the order above is correct, then on the live Supabase data:

- fingerprint punches (`VERIFY=1`) were treated as check-outs: an `UPDATE` on a day with no row
  matches nothing, so those employees have **no attendance at all**;
- card punches (`4`) were treated as overtime check-ins and face punches (`15`) were ignored.

Run on the production database (Supabase SQL editor) and compare with the device's own
attendance report for the same days:

```sql
-- Active employees with no attendance at all in the last 30 days.
-- A cluster of fingerprint users here is the symptom.
SELECT e.id, e.first_name, e.last_name, e.cedula
FROM employees e
WHERE e.status = 'active' AND e.cedula IS NOT NULL
  AND NOT EXISTS (
    SELECT 1 FROM attendance_logs a
    WHERE a.employee_id = e.id AND a.date >= current_date - 30
  );
```

If production is affected, record the date range and the affected employees. Backfilling is a
separate decision for the HR owner; this change does not rewrite history.

Findings (fill in): `__________________________________________________`

**Sources** (from research note `sdd/rrhh-go-auth-cutover/adms-auth-research`, not independently
re-verified here): Security PUSH protocol 3.1.2 specification (pdfcoffee mirror); GitHub
`s0x90/zkteco-adms`; GitHub `dewanlab/laravel-zkteco-adms-core`; GitHub
`mmd-rehan/ADMS-server-ZKTeco`; the standtech.tech ADMS guide. The "Attendance PUSH 2020" PDF
(Scribd) could not be read. These are third-party descriptions: the device capture is the
authority.

### B2. Device authentication decision (CO-S5b-1)

**Why it blocks.** `POST /iclock/cdata` authenticates a device **only by its serial number**
(`SN`). Anyone who knows a claimed serial and an employee cedula can forge punches for that
company over plain HTTP. This is the same exposure as the Edge Function; it is no longer
acceptable to carry it into production unexamined.

Research finding: legacy ADMS (`/iclock/cdata?SN=`) has no per-request authentication beyond
`SN`. Newer firmware may support an optional `pushcommkey` (derived from the device Comm Key;
algorithm unverified). The Security PUSH 3.1.2 protocol adds `POST /iclock/registry` with an
`authKey`, then a `token = MD5(RegistryCode + SN + SessionID)` cookie on each request; it is a
different protocol (real-time log), per-model firmware support is unverified and the spec does
not mention TLS. Devices only let you configure address and port (HTTPS on newer firmware); no
evidence of custom paths or query strings. Most open-source ADMS servers do no device
authentication; some put Apache Basic auth in front.

Options, ranked (decide, then record the choice and the reason below):

| Rank | Option | What it gives | Limits |
|------|--------|---------------|--------|
| 1 | Operator pre-registration and **no auto-create** | A device must be registered by an admin before it is accepted; unknown serials are refused, not pooled. Works on all firmware. Also closes B3. | Does not stop forgery with a known serial. Implemented: an unknown `SN` is refused with `403` and nothing is written. |
| 2 | Comm Key (`pushcommkey`) or Security PUSH token | The only cryptographic binding of a request to a device. | Needs a real-device test; firmware-dependent; protocol work in the backend. |
| 3 | IP allowlist or VPN per site | Restricts who can reach `/iclock/`. | Breaks behind shared or changing NAT; operational upkeep. |
| 4 | TLS only | Prevents sniffing and tampering in transit. | No device identity; firmware support to verify. |
| 5 | Anomaly detection (counters, alerts) | Detects abuse after the fact using `zkteco_ingest_rejections` and `adms_batch` logs. | Detective, not preventive. |

Recommended minimum for go-live: rank 1, plus rank 3 or 2 if the device and network allow it.

Decision: **rank 1, implemented on 2026-10-15** (branch `feat/device-preregistration`). Every ADMS
endpoint answers `403` to a serial with no row in `zkteco_devices` and writes nothing; an admin
registers a device first (Configuración > ZKTeco > Registrar, or `POST /api/zkteco_devices`). Reason:
it works on every firmware and closes B3 at the source. **Still open:** a forged request with a
registered serial is accepted. Decide rank 2 (Comm Key) or rank 3 (allowlist/VPN) once a real device
and the site network can be tested.

### B3. Unclaimed-pool flooding mitigation (CO-S5b-2)

**Why it blocks.** Any caller can create a `zkteco_devices` row by sending a heartbeat with a new,
well-formed serial. The only limiter is the per-IP rate limit (600 requests/minute by default),
which allows roughly 864,000 new unclaimed rows per day from one address and buries real
devices in the admin "unclaimed devices" list.

Mitigations (pick at least one, record it below):

- Rank 1 of B2 (pre-registration, no auto-create) removes the problem at the source.
- Otherwise cap the pool: refuse heartbeats from unknown serials once `count(*) WHERE company_id
  IS NULL` passes a limit, and expire unclaimed rows that never pushed a punch.
- Meanwhile, monitor: `SELECT count(*) FROM zkteco_devices WHERE company_id IS NULL;` and purge
  junk by hand.

Decision: **closed by B2 rank 1** (2026-10-15). An unknown serial never creates a row, so the
unclaimed list only holds devices the importer loaded without a company (step 9). Its refusals are
not logged per request; the per-IP ADMS rate limit stays the only throttle.

### Other OPS-1 items to verify on the device

Record the observed value next to each; revise the S5 constants if they differ.

| Item | Current assumption (provisional) | Observed |
|------|----------------------------------|----------|
| Acknowledgement bodies | `OK` for heartbeat/poll/other tables, `OK: <n>` for ATTLOG | |
| Option text and `ATTLOGStamp` | Copied from the Edge Function (`admsOptionText`) | |
| Paths | `/iclock/cdata`, `/iclock/getrequest`; `/iclock/devicecmd` answered `OK` | |
| Punch-code mapping | 0/4 check-in, 1/5 check-out; others rejected `unsupported_code` | |
| Firmware version | Not parsed in v1 | |
| Transport | HTTP on port 80 (or the configured port), no TLS | |
| Rate limit | 600 requests/minute per client IP | |

The rate-limit row matters when several devices sit behind one customer NAT: they share one
bucket. A device polls `getrequest` about every 10 seconds and pushes once or twice a minute,
about 8 requests per minute, so 600 covers roughly 75 devices behind one address. Measure real
traffic and raise `ADMS_RATE_PER_MIN` if needed.

Public host and port for devices (OPS-2): `____________________`. The shipped `docker-compose.yml`
publishes nginx on `8100`; many firmwares expect port 80. Decide before step 11.

---

## Steps

### 1. Write freeze and Supabase export `[USER-OPS]` (OPS-3)

1. Announce a write freeze and make Supabase read-only for users (disable signups, tell staff to
   stop editing). Devices may keep pushing to the Edge Function until step 11; re-export attendance
   after the freeze if you need those punches in Go.
2. Export every table to `<table>.json` in one directory as described in `backend/README.md`,
   section "Importing data from Supabase", step 1. The examples below call it `export/` at the
   repository root; keep it out of version control.
3. Keep the export directory private. It contains personal data.

### 2. Compare `auth.users` with `public.users` `[USER-OPS]` (OPS-4)

Admins who registered through the old sign-up may exist in Supabase Auth without a row in
`public.users`. They could not log in to the Go backend, which only knows `public.users`.

```sql
-- Supabase SQL editor. Every row returned is an account that would be lost.
SELECT au.id, au.email, au.created_at
FROM auth.users au
LEFT JOIN public.users pu ON pu.id = au.id
WHERE pu.id IS NULL;
```

For each admin returned, create the row **in Supabase before exporting**, using the company the
sign-up created, then re-export `users`:

```sql
INSERT INTO public.users (id, company_id, email, role, first_name, last_name, status)
VALUES ('<auth.users id>', '<company id>', '<email>', 'admin', '<first>', '<last>', 'active');
```

Gate: the query above returns zero rows (or each remaining row is a confirmed non-user you
accept to drop). Do not continue otherwise.

### 3. Case-insensitive duplicate-email check `[USER-OPS]` (OPS-5)

Migration `0010` creates a unique index on `lower(email)` and aborts, naming the addresses, if
duplicates exist. Find them first.

```sh
# Against the export (the importer also refuses duplicates).
jq -r '.[] | .email // empty | ascii_downcase' export/users.json | sort | uniq -d
```

```sql
-- Against the Go database.
SELECT lower(email) AS email, count(*)
FROM users WHERE email IS NOT NULL
GROUP BY 1 HAVING count(*) > 1;
```

Gate: both return nothing. Resolve duplicates by deactivating or renaming one account in the
source of truth, then re-export.

### 4. Apply every missing migration `[USER-OPS]` (OPS-5)

Postgres runs `docker-entrypoint-initdb.d` **only on a fresh volume**. An existing volume keeps
its old schema, and the dev volume was found missing `0005` to `0009`, so assume yours is too.
Apply **every migration missing on the volume, in order**, not just `0010` to `0016`. Files
`0005` and later are idempotent, so applying one that is already present is harmless.

```sh
cd backend
for f in 0005_lookup_unique 0006_phase2_columns 0007_phase3_payroll 0008_phase4_hr_records \
         0009_medical_permission 0010_users_email_lower_unique 0011_zkteco_ingestion \
         0012_zkteco_tenant_scoping 0013_users_token_version 0014_payroll_periods \
         0015_applications_unique 0016_enrollments_unique; do
  echo "applying $f"
  docker compose exec -T postgres \
    psql -U rrhh_go -d rrhh_go -v ON_ERROR_STOP=1 -f "/docker-entrypoint-initdb.d/$f.sql" \
    || { echo "STOP: $f failed"; break; }
done
```

`ON_ERROR_STOP=1` makes the first failure abort that file. Stop at the first failure and fix
the cause before going on: `0005` and `0007` create unique indexes that fail if existing rows
violate them, and `0010` fails on duplicate emails (step 3).

`UpsertUnknownPin` needs the unique constraint created by `0012`
(`uq_zkteco_unknown_pins_serial_pin`). Without it, every ATTLOG batch that contains an unknown PIN
fails with SQL error `42P10` and rolls the whole batch back (CO-S5b-3). There is no startup
check for this, so verify explicitly.

`0013` adds `users.token_version`, which the API reads on **every authenticated request**
(session check). Apply it **before** starting the new binary: without the column every
protected route answers 500.

`0014` adds `employee_pay_records.period` and the unique key
`payroll_history (company_id, year, month, period)`. The unique index fails if the table already
holds duplicate runs for one period. Check first and delete the older duplicates:

```sql
SELECT company_id, year, month, period, count(*) FROM payroll_history
GROUP BY 1,2,3,4 HAVING count(*) > 1;
```

If `0014` stops on that index, nothing the running binary needs is gone yet: the old run index
`uq_epr_run_period` is dropped only after both new indexes exist. Delete the duplicates and apply
the file again.

Apply it **before** starting the new binary (the run upsert writes the `period` column).

`0015` adds the unique key `applications (job_posting_id, candidate_id)`: one application per
candidate and posting, so a retried create answers `409` instead of writing a second row. The
index fails if two applications already share a posting and a candidate. Check first, keep one
row per pair (normally the most advanced stage) and delete the others:

```sql
SELECT job_posting_id, candidate_id, count(*) FROM applications
WHERE job_posting_id IS NOT NULL AND candidate_id IS NOT NULL
GROUP BY 1,2 HAVING count(*) > 1;
```

Nothing the running binary needs depends on `0015`: if it stops on the index, delete the
duplicates and apply the file again. The importer (step 7) rejects a repeated pair in the export
as `duplicate (job_posting_id, candidate_id) in export`.

`0016` adds the unique key `enrollments (training_id, employee_id)`: an employee is enrolled at
most once per course. It fails on existing duplicates; check first and keep one row per pair
(normally the graded one):

```sql
SELECT training_id, employee_id, count(*) FROM enrollments
WHERE training_id IS NOT NULL
GROUP BY 1,2 HAVING count(*) > 1;
```

Nothing the running binary needs depends on `0016`; the trainings endpoints work without it, they
just cannot refuse a duplicate. The importer rejects a repeated pair in the export as
`duplicate (training_id, employee_id) in export`.

**Verify** by comparing the catalog with `backend/migrations/`. Every row must be `true`. Note:
`0009` reads `true` on an empty `roles` table whether or not it ran; that is harmless, since
applying it is idempotent.

```sql
SELECT m.file, m.applied FROM (VALUES
  ('0005_lookup_unique',
     to_regclass('idx_role_co_name_lower') IS NOT NULL),
  ('0006_phase2_columns',
     EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'attendance_logs' AND column_name = 'work_type')
     AND EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'deductions' AND column_name = 'numero_planilla')),
  ('0007_phase3_payroll',
     EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'employee_pay_records' AND column_name = 'origin')
     AND EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'liquidation_history' AND column_name = 'calc_version')),
  ('0008_phase4_hr_records',
     EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'medical_records' AND column_name = 'cost')
     AND to_regclass('idx_unif_co') IS NOT NULL),
  ('0009_medical_permission',
     NOT EXISTS (SELECT 1 FROM roles WHERE jsonb_typeof(permissions) IS DISTINCT FROM 'object' OR NOT permissions ? 'medical')),
  ('0010_users_email_lower_unique',
     to_regclass('idx_users_email_lower') IS NOT NULL),
  ('0011_zkteco_ingestion',
     to_regclass('zkteco_ingest_rejections') IS NOT NULL),
  ('0012_zkteco_tenant_scoping',
     to_regclass('uq_zkteco_unknown_pins_serial_pin') IS NOT NULL AND to_regclass('idx_emp_cedula') IS NOT NULL),
  ('0013_users_token_version',
     EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'users' AND column_name = 'token_version')),
  ('0014_payroll_periods',
     to_regclass('uq_payroll_history_period') IS NOT NULL AND to_regclass('uq_epr_run_period_v2') IS NOT NULL),
  ('0015_applications_unique',
     to_regclass('uq_applications_posting_candidate') IS NOT NULL),
  ('0016_enrollments_unique',
     to_regclass('uq_enrollments_training_employee') IS NOT NULL)
) AS m(file, applied)
ORDER BY m.file;
```

If you add a migration later, add its row here and to the loop above.

### 5. Back up the Go database `[USER-OPS]` (OPS-5)

```sh
docker compose exec -T postgres pg_dump -U rrhh_go -d rrhh_go -Fc > rrhh_go_pre_cutover.dump
```

Test-restore the dump into a scratch database before relying on it.

### 6. Delete the seed admin `[USER-OPS]` (OPS-6)

`migrations/0003_seed.sql` inserts the real Ventatec admin id with a **known development password**
(`Admin123`). The importer skips users whose id already exists, so a plain import would leave that
password on the real admin. Delete the seed row first; the import then recreates the admin from the
Supabase export with a temporary password. This is why step 2 matters: if the admin is missing from
the export, you would lock yourself out.

```sql
-- Preflight: lists the seed credential (any status; inactive rows count). Must show it before, and nothing after.
SELECT id, email, status FROM users
WHERE password_hash = '$2a$10$d8UdlIbA4nESHywDqYk6h.L2W6qk9YEUiPqNnUm5qgQmKKGAwk9Q.';

DELETE FROM users
WHERE password_hash = '$2a$10$d8UdlIbA4nESHywDqYk6h.L2W6qk9YEUiPqNnUm5qgQmKKGAwk9Q.';
```

Run them through a quoted heredoc so the shell never expands `$2a$10$...` (with `-c "..."` the
shell would replace `$2`, `$10` and the rest with empty strings, the preflight would return zero
rows and the delete would remove nothing):

```sh
docker compose exec -T postgres psql -U rrhh_go -d rrhh_go -v ON_ERROR_STOP=1 <<'SQL'
SELECT id, email, status FROM users
WHERE password_hash = '$2a$10$d8UdlIbA4nESHywDqYk6h.L2W6qk9YEUiPqNnUm5qgQmKKGAwk9Q.';
SQL
```

Run the `DELETE` the same way, then run the preflight again. No table references `users(id)`, so
the delete is safe. **Cutover is not declared done until the preflight returns zero rows**; step 10 enforces
it at startup, and it also refuses an inactive row (setting `status='inactive'` is not a substitute
for the delete). With any `APP_ENV` other than `production` the guard is skipped and the log has one
`seed guard skipped` line with the value it saw; if you expected production, that line means a typo.

### 7. Import: dry run, then real run `[USER-OPS]` (OPS-7)

Full flag reference: `backend/README.md`, "Importing data from Supabase".

```sh
cd backend
export DB_URL="postgres://rrhh_go:<password>@localhost:5434/rrhh_go?sslmode=disable"

# 1. Dry run: validates and reports, then rolls back. Writes nothing, not even the CSV.
go run ./cmd/import --dir ../export --db-url "$DB_URL" --dry-run

# 2. Real run. One transaction: any error rolls everything back.
go run ./cmd/import --dir ../export --db-url "$DB_URL" --passwords-out ../import-passwords.csv
ls -l ../import-passwords.csv        # expect -rw------- (0600)
```

If the dry run reports `applications` rows as `duplicate (job_posting_id, candidate_id) in export:
same as row <id>`, the same candidate applied twice to one posting (one application per pair since
`0015`). The importer keeps the first row in file order, which is not necessarily the right one:
compare both ids in the export, delete the one you do not want from the export, and run the dry run
again before the real run. Do not reach for `--skip-invalid` to get past it.

The importer is insert-only and safe to re-run on the same export (SR-4): it creates no
duplicates, never rewrites the password of an existing user and does not write a second
passwords file. It refuses to overwrite an existing `--passwords-out` file, so use a new name on
each run that creates users.

If the CSV is lost after a successful run, the passwords cannot be regenerated by re-running (the
users already exist). Delete the affected user rows and import again.

Devices are imported with `company_id` `NULL`: they are not attached to a company yet (step 9).

### 8. Distribute temporary passwords `[USER-OPS]` (OPS-7)

Treat `import-passwords.csv` as a secret. Hand each person their own password over a secure
channel, then delete the file and any copies. Its name matches `*passwords*.csv`, which is in
`.gitignore`.

### 9. Claim imported devices `[USER-OPS]` (OPS-8)

Imported devices have no owner, so **no attendance is ingested from them until they are claimed**
(punches are counted as `device_unclaimed`). Pick one:

- **Per device (default):** log in as the company admin, open Configuración > ZKTeco, and claim
  each device from the unclaimed list (or `POST /api/zkteco_devices/{id}/claim`).
- **Devices that are not in the export** are registered, not claimed: Configuración > ZKTeco >
  Registrar with the serial number the device shows (or `POST /api/zkteco_devices`). ADMS refuses
  any serial that has no row (B2).
- **Guarded single-company backfill**, only if exactly one company exists. The `WHERE` clause makes
  the statement do nothing when a second company is present:

```sql
UPDATE zkteco_devices
SET company_id = (SELECT id FROM companies)
WHERE company_id IS NULL
  AND (SELECT count(*) FROM companies) = 1;
```

Claiming deletes the rejection counters and unknown PINs collected for that serial while it was
unclaimed, so the new owner starts clean.

Gate: `SELECT serial_number FROM zkteco_devices WHERE company_id IS NULL;` returns only devices
you intentionally leave unclaimed.

### 10. Deploy with `APP_ENV=production` `[USER-OPS]` (OPS-9)

1. Set the production environment (see `backend/env.example`): strong `POSTGRES_PASSWORD` and
   `JWT_SECRET`, `APP_ENV=production`, `AUTH_RATE_PER_MIN`, `AUTH_ATTEMPT_RATE_PER_MIN`,
   `ADMS_RATE_PER_MIN`, and `TRUSTED_PROXY_CIDRS` matching your proxy network.
2. If you changed `nginx/default.conf`, **reload nginx**. The file is bind-mounted and
   `docker compose up` does not re-read it:

```sh
cd backend
docker compose up -d --build
docker compose exec nginx nginx -s reload
curl -s http://<host>/api/health
```

3. Confirm the startup guard: the backend log must **not** contain `seed credential present`. If it
   does, the container exits 1; go back to step 6.

### 11. Repoint devices `[USER-OPS]` (OPS-8)

Only after blockers B1 to B3 are closed and step 9 is done: every device must be claimed or
registered first. A serial with no row is refused with `403`; the device keeps its punches and
retries, but nothing is recorded until it is registered. On each device set the cloud/ADMS
server address to `http://<host>/iclock/` and the agreed port (OPS-2). The Edge Function stays
deployed; to roll a device back, point it at the Edge Function again.

Verify with one real punch per claimed device: it must appear in attendance within a minute.
If it does not, check `zkteco_ingest_rejections` for that `serial_number`:

```sql
SELECT reason, count, last_seen FROM zkteco_ingest_rejections WHERE serial_number = '<SN>';
```

### 12. Run the end-to-end checklist `[USER-OPS]` (OPS-9)

Complete the [E2E checklist](#e2e-checklist-sr-5) below, including demo mode.

### 13. Stabilization window, 14 days minimum `[USER-OPS]` (OPS-10)

Keep the Supabase project and the deployed Edge Function **live** for at least 14 days after the
flip. Do not delete, pause or downgrade either before day 14. During the window watch:

- login failures and 429s (`rate limited` WARN logs);
- `zkteco_ingest_rejections` growth per serial, and `adms_batch` log lines;
- `SELECT count(*) FROM zkteco_devices WHERE company_id IS NULL;` (pool flooding, B3).

---

## Rollback `[USER-OPS]`

| Situation | Action |
|-----------|--------|
| Frontend or login broken after the flip | Revert the tracker merge on `master`; the app returns to Supabase login. |
| Devices misbehaving | Repoint them to the Edge Function URL. |
| Bad migration | First repoint every device away from Go (row above): ADMS ingestion writes `zkteco_ingest_rejections` in the same transaction as each batch, so dropping that table while devices still push makes every batch with a rejection fail with 500 and retry forever. Then `DROP INDEX idx_users_email_lower;` and, if needed, `DROP TABLE zkteco_ingest_rejections;`. Restore the step 5 dump for anything else. |

Limits you must accept before starting: **writes made through the Go backend after the flip are not
carried back to Supabase**. Do not decommission Supabase within 14 days. Decide in advance who may
call the rollback and what happens to data entered in Go meanwhile.

---

## E2E checklist (SR-5)

`[USER-OPS]` Run in a real browser against the deployed environment, once in **real mode** and once in
**demo mode**. Use a private window so no old session leaks in. Items marked "no request" are
verified in the browser DevTools Network tab.

The session lifecycle is also covered by an automated harness that runs the real functions from
`hr_admin_panel.html` against a fake fetch (`node tools/session/harness.mjs`, exit code 1 on failure):
login success and failures (401, 429 with `Retry-After`, network), register auto-login, logout with no
request, expired, malformed and valid tokens on load, one toast on concurrent 401s, and demo mode
with zero requests. Run it before the checklist; the items marked "browser-only" below are what it
cannot see (real DOM, nginx headers, timers, layout).

### Real mode

- [ ] **Login:** valid email and password lands in the app; the Network tab shows `POST /api/auth/login`
      returning 200.
- [ ] **Login failure:** a wrong password shows "Credenciales inválidas"; an unknown email shows the same
      text; neither is treated as an expired session.
- [ ] **Login rate limit:** more than 10 failed attempts in a minute from one address returns 429 and the
      screen shows "Demasiados intentos, intenta en N s" (browser-only: the seconds come from the
      `Retry-After` header, so check that nginx passes the header through and the number is plausible).
- [ ] **Register:** "Registrar nueva empresa" creates the company and lands in the app already logged in
      as the new admin, with no second login step.
- [ ] **Logout:** returns to the login view, removes the session keys from `localStorage`, and sends **no
      request** (verify in Network).
- [ ] **Token expiry on load:** with an expired token in `localStorage` (edit `hr_sess` to an old token),
      reload: the login view appears and **no API call** is made.
- [ ] **401 mid-session:** invalidate the token while logged in (change `JWT_SECRET` in a test
      environment, or edit the stored token), trigger any action: you are returned to login with a
      single "Sesión expirada" toast. (browser-only) Also confirm the screen you were on does not stay
      stuck on a spinner, any modal that was open is gone or harmless, and the Network tab shows no
      further attendance polling requests once the login view is up.
- [ ] **Old Supabase session (browser-only):** in a browser that was logged in before the flip, reload
      the new build. The stored Supabase token is not accepted by Go: you get the login view (or, if its
      `exp` is still in the future, one "Sesión expirada" toast on the first Go call). Either way log in
      again and the app works; no loop of toasts.
- [ ] **Login view layout (browser-only):** the login view shows "Modo Demo" under "Registrar nueva
      empresa"; there is no way to reach a blank screen (no setup screen on first load once S7c is
      deployed). The red error alert above the email field is visible for a wrong password and the
      "Entrar" button returns from its spinner.
- [ ] **Persistence per screen:** on each screen below, create or edit a record, reload the page, and
      confirm it comes back from the server with its server id:
  - [ ] Employees and attendance
  - [ ] Recruitment (job postings, candidates, applications, hire a candidate)
  - [ ] Users (the temporary password is shown once, then not retrievable)
  - [ ] ZKTeco (device list, unclaimed devices, claim, unknown PINs)
  - [ ] Payroll, deductions, leave, evaluations, medical records, uniforms, documents
- [ ] **ZKTeco punch:** a real punch from a claimed device appears under attendance and in the live feed;
      the same punch pushed again creates no duplicate.
- [ ] **Unclaimed device:** a punch from an unclaimed device creates no attendance and increments
      `device_unclaimed` in `zkteco_ingest_rejections`.
- [ ] **Non-admin:** a non-admin user cannot manage users, edit roles or claim devices (the API answers
      403; the ZKTeco screen shows "Solo administradores").
- [ ] **Tenant isolation:** two companies cannot see each other's rows.
- [ ] **Startup guard:** the backend log has no `seed credential present`, and the seed login
      (`livan.05@hotmail.com` / `Admin123`) is rejected.

### Demo mode (regression)

Demo mode must behave exactly as before the change and make **no network request** to Go or Supabase
(verify in Network).

- [ ] Demo login works.
- [ ] Demo register works.
- [ ] Recruitment, Users, ZKTeco and Attendance screens load their demo data; edits stay local.
- [ ] ZKTeco "Probar conexión" explains that it is unavailable in demo mode.
- [ ] Logout returns to the login view with **no request**.
- [ ] (browser-only) On the login view "Modo Demo" turns into "Salir del modo demo" once demo is on;
      pressing it returns to real mode, `localStorage.hr_demo` is gone, and a reload shows the real
      login with no demo banner or data.
- [ ] (browser-only) Reload while inside the demo app: you stay in demo (no expiry check on the fake
      token, no login redirect).

---

## Supabase removal grep gate (SR-1)

Run from the repository root. Both commands must print **nothing** and exit 1 (`rg` exit code 1 =
no match). SR-1 is about *application* code, so the gate names the runtime paths explicitly instead
of scanning the whole tree.

```bash
# Gate A: browser app and deploy files. Any mention at all is a failure.
rg -n -i 'supabase|/auth/v1|/rest/v1|apikey|hr_go_tables|swciybuhetkfrtpyjvgb|DEFAULT_URL|DEFAULT_KEY|saveConfig|showSQLSchema|\bA\.(url|key)\b' \
  hr_admin_panel.html backend/nginx backend/docker-compose.yml backend/Dockerfile \
  backend/env.example backend/Makefile

# Gate B: Go runtime. Endpoints, keys and env names of Supabase must not appear.
rg -n -i '/auth/v1|/rest/v1|apikey|supabase\.co|supabase_|supabase-js|hr_go_tables' \
  backend/cmd/api backend/internal --glob '!backend/internal/importer/**'
```

**Deliberately not scanned (and why):**

| Path | Why it may name Supabase |
|------|--------------------------|
| `backend/cmd/import/`, `backend/internal/importer/` | The importer reads a Supabase **export** (JSON files) and says so. It never talks to Supabase. |
| `backend/migrations/` | SQL history and comments (`0001_init.sql` is the Supabase schema, ported). |
| `docs/`, `CHANGELOG.md`, `openspec/`, `odd/`, `backend/README.md` | Documentation and history, including this runbook and the importer procedure. |
| `README.md`, `schema.sql` (repository root) | Legacy documentation and the original Supabase schema kept as reference. Not loaded by any runtime. |
| `tools/` | Test harnesses. `tools/session/harness.mjs` asserts that **no** request goes to Supabase. |
| Comments in `backend/internal/**` (outside gate B's patterns) | Historical notes such as "replaces Supabase's RLS" or "parity with the retired Edge Function". They are comments, not references. |

Gate B is deliberately narrower than gate A: the bare word "Supabase" appears in Go comments that
describe history, so only endpoints, header names, hostnames and env var names are forbidden there.

**Deleting `supabase/` does not undeploy anything.** The `supabase/` directory held the source of
the `zkteco-adms` Edge Function. Removing it from the repository does **not** remove the deployed
function or the Supabase project: both must stay live for the 14-day stabilization window (step
13). Recover the source with `git log --diff-filter=D -- supabase/` and
`git show <sha>^:supabase/functions/zkteco-adms/index.ts`. The commit that introduced it is
`4a741fa`, so `git show 4a741fa:supabase/functions/zkteco-adms/index.ts` works at any time.

---

## Decisions to record before go-live

| Item | Decision | Date | By |
|------|----------|------|----|
| B1 field order confirmed, parser index | | | |
| B1 production data check result | | | |
| B2 device authentication | | | |
| B3 unclaimed-pool mitigation | | | |
| OPS-2 public host and port | | | |
| Rollback owner | | | |
