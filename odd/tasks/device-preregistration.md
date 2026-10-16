# Device pre-registration (runbook B2 rank 1, closes B3)

Objective: a ZKTeco device must be registered by an admin before ADMS accepts it; unknown serials never create rows.
Branch: `feat/device-preregistration` (from master e32ef78). Delivery: ask-on-risk. Tests: Go `go test ./...` with `TEST_DB_URL`; frontend node harness under `tools/`.

## Specs

- **S1** ADMS refuses unknown serials. `GET /iclock/cdata`, `GET /iclock/getrequest`, `POST /iclock/cdata` and `POST /iclock/devicecmd` with a well-formed `SN` that is not in `zkteco_devices` answer `403` in the API error envelope (`error.code` `forbidden`, like the other ADMS errors) and write nothing (no `zkteco_devices` row, no attendance, no unknown-pin or rejection rows). A serial already in `zkteco_devices` (registered, or imported and still unclaimed) keeps today's behavior.
- **S2** `POST /api/zkteco_devices` (admin only, `403` otherwise) registers a device for the caller's company: `serial_number` required (same format rule ADMS applies to `SN`), `name` and `location` optional (1-200 characters or null). `201` with a one-element array. A serial that already exists (any company, or unclaimed) is `409 conflict`. The company comes from the token, never from the body; unknown fields are `400`.
- **S3** Panel "Relojes": an admin can register a device (serial, name, location) from the page; the "Relojes sin asignar" card stays for imported devices, and its footer no longer says a new device registers itself.
- **S4** Docs: backend README ZKTeco sections describe registration and the 403; runbook B2 and B3 record the decision (rank 1, pre-registration, no auto-create) and steps 9/11 say to register a device before repointing it.
- Assumption (no user decision asked, recommended option accepted: "Dale con tu siguefencias"): unknown-serial requests are not logged per request, to avoid log flooding; the per-IP rate limit stays the only throttle.

## Tasks

| ID | Specs | Route | Status | Evidence |
|----|-------|-------|--------|----------|
| T1 | S1, S2, S4 (README) | inline | done | f782d64; RED: unknown SN 200, POST 405; GREEN: suite 12/12 ok |
| T2 | S3 | inline | done | RED: registerZKDevice missing; GREEN: tools/zk-register 6/6, all harnesses ok |
| T3 | S4 (runbook) | inline | pending | |
| V  | S1-S4 | delegated verifier (high risk: item 2, device guard) | pending | |

## Log

- **L1** (2026-10-15) User: "Dale con tu siguefencias" — answering "¿Vamos con la opción 1, pre-registro sin auto-alta?", after "NO tengo acceso al reloj" and "Arranca".
- **L2** Map: heartbeat self-registers via `UpsertDeviceHeartbeat` (adms.sql), called from `ADMSOptions`, `ADMSGetRequest` and the `POST /iclock/cdata` tx (backend/internal/handlers/adms.go:106,120,202). Claim flow: `GET /api/zkteco_devices/unclaimed`, `POST /api/zkteco_devices/{id}/claim`; panel `zkRenderUnclaimed`/`claimZKDevice` (hr_admin_panel.html:4401-4460). The importer loads `zkteco_devices` (company may be NULL, runbook step 9).
- **L3** S1 revised (agent decision, consistency): the 403 uses the A2 error envelope like the existing ADMS 400/500 answers, not plain text.
