// Package migrations exposes the SQL of migrations that other binaries need
// to re-run at runtime. The migration runner itself does not use it.
package migrations

import _ "embed"

// MedicalPermission is 0009_medical_permission.sql. It is idempotent: it only
// touches roles whose permissions lack the `medical` key, so the Supabase
// import re-runs it after loading roles.
//
//go:embed 0009_medical_permission.sql
var MedicalPermission string
