// Package scheduler owns the vacation-accrual job (design Q5, phase2-design):
// a Go-side, idempotent-by-construction replacement for the retired
// accrue_vacation_days() plpgsql function and its commented pg_cron
// schedule. It exposes a daily ticker (Run) plus the two query entry points
// an HTTP handler or the ticker may call (AccrueAll, AccrueCompany).
package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	db "github.com/livant05/rrhh-go/internal/db/generated"
)

// panamaLocation is America/Panama: a fixed UTC-5 offset with no DST
// (design Q5b). The accrual year MUST be resolved in this timezone, not the
// container's local time -- a 02:00-UTC run on January 1st is still December
// 31st in Panama, and would otherwise accrue against the wrong year.
var panamaLocation = time.FixedZone("America/Panama", -5*60*60)

// advisoryLockKey is an arbitrary, stable application-level
// pg_try_advisory_lock key (design Q5c). Two API replicas racing the same
// scheduled tick would otherwise both execute AccrueAll concurrently --
// harmless in outcome, since AccrueVacationDays is idempotent by
// construction, but a live source of lock contention and ON CONFLICT
// retries. No leader election, no new dependency.
const advisoryLockKey = 72190001

// Scheduler owns the accrual job. AccrueAll's nil company_id is a named A1
// exception (design Q5b): it is reachable only from Run, via runOnce, never
// from an HTTP handler -- AccrueCompany's companyID parameter is
// non-nullable by signature, so no caller can pass nil even by mistake.
type Scheduler struct {
	pool    *pgxpool.Pool
	queries *db.Queries
	log     *slog.Logger
	enabled bool
	hour    int
}

// New builds a Scheduler. enabled/hour come from the ACCRUAL_ENABLED
// (default true) / ACCRUAL_HOUR (default 2) env vars (design Q5c), resolved
// by cmd/api/main.go.
func New(pool *pgxpool.Pool, queries *db.Queries, log *slog.Logger, enabled bool, hour int) *Scheduler {
	return &Scheduler{pool: pool, queries: queries, log: log, enabled: enabled, hour: hour}
}

// AccrueAll recomputes earned_days for every active employee across every
// tenant (company_id = NULL -- design Q5b's named A1 exception). Returns the
// number of leave_balances rows upserted. Only Run (via runOnce) calls this;
// no HTTP handler can reach it.
func (s *Scheduler) AccrueAll(ctx context.Context) (int, error) {
	rows, err := s.queries.AccrueVacationDays(ctx, db.AccrueVacationDaysParams{
		Year:      currentPanamaYear(),
		CompanyID: pgtype.UUID{}, // Valid=false -> NULL, matches sqlc.narg('company_id')
	})
	if err != nil {
		return 0, err
	}
	return len(rows), nil
}

// AccrueCompany recomputes earned_days for every active employee within one
// tenant. companyID is non-nullable by signature, so this is the only
// accrual entry point an HTTP handler (POST /api/leave_balances/accrue) may
// call -- the cross-tenant AccrueAll path is structurally unreachable from
// there (design Q5b/Q5c).
func (s *Scheduler) AccrueCompany(ctx context.Context, companyID pgtype.UUID) ([]db.LeaveBalance, error) {
	return s.queries.AccrueVacationDays(ctx, db.AccrueVacationDaysParams{
		Year:      currentPanamaYear(),
		CompanyID: companyID,
	})
}

// currentPanamaYear resolves "the current calendar year" the way the ported
// formula requires: in America/Panama, not the process's local/UTC time.
func currentPanamaYear() int32 {
	return int32(time.Now().In(panamaLocation).Year())
}

// Run blocks until ctx is cancelled, running the accrual job once a day at
// the configured America/Panama hour (design Q5c). It computes
// time.Until(next occurrence) and re-arms after each run rather than using a
// bare time.NewTicker(24*time.Hour), which would drift to whatever time the
// process last restarted. There is no run at startup: a crash-looping
// container must not re-run the job every few seconds -- the manual
// POST /api/leave_balances/accrue endpoint is the on-demand path.
//
// When ACCRUAL_ENABLED=false, Run is a no-op that still blocks on
// ctx.Done(), so cmd/api/main.go's shutdown lifecycle (close a "done"
// channel once Run returns) behaves identically whether the scheduler is
// enabled or not.
func (s *Scheduler) Run(ctx context.Context) {
	if !s.enabled {
		s.log.Info("accrual scheduler disabled (ACCRUAL_ENABLED=false)")
		<-ctx.Done()
		return
	}

	for {
		wait := s.untilNextRun(time.Now())
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			s.runOnce(ctx)
		}
	}
}

// untilNextRun returns the duration from now until the next occurrence of
// the configured hour in America/Panama, strictly in the future: if now is
// already past today's run time, the next run is tomorrow at the same hour.
func (s *Scheduler) untilNextRun(now time.Time) time.Duration {
	nowP := now.In(panamaLocation)
	next := time.Date(nowP.Year(), nowP.Month(), nowP.Day(), s.hour, 0, 0, 0, panamaLocation)
	if !next.After(nowP) {
		next = next.AddDate(0, 0, 1)
	}
	return next.Sub(nowP)
}

// runOnce attempts the advisory lock (design Q5c) before running AccrueAll.
// If the lock is not acquired, another replica already holds it for this
// tick: log and skip rather than run concurrently.
func (s *Scheduler) runOnce(ctx context.Context) {
	var acquired bool
	if err := s.pool.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", advisoryLockKey).Scan(&acquired); err != nil {
		s.log.Error("accrual: advisory lock query failed", "err", err)
		return
	}
	if !acquired {
		s.log.Info("accrual: advisory lock held by another replica, skipping this tick")
		return
	}
	defer func() {
		if _, err := s.pool.Exec(ctx, "SELECT pg_advisory_unlock($1)", advisoryLockKey); err != nil {
			s.log.Error("accrual: advisory unlock failed", "err", err)
		}
	}()

	n, err := s.AccrueAll(ctx)
	if err != nil {
		s.log.Error("accrual run failed", "err", err)
		return
	}
	s.log.Info("accrual run complete", "rows", n)
}
