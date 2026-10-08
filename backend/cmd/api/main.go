package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/auth"
	"github.com/livant05/rrhh-go/internal/handlers"
	"github.com/livant05/rrhh-go/internal/scheduler"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	dbURL := os.Getenv("DB_URL")
	if dbURL == "" {
		log.Error("DB_URL not set")
		os.Exit(1)
	}
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		log.Error("JWT_SECRET not set")
		os.Exit(1)
	}

	accrualEnabled, err := parseBoolEnv("ACCRUAL_ENABLED", true)
	if err != nil {
		log.Error("invalid ACCRUAL_ENABLED", "err", err)
		os.Exit(1)
	}
	accrualHour, err := parseIntEnv("ACCRUAL_HOUR", 2)
	if err != nil {
		log.Error("invalid ACCRUAL_HOUR", "err", err)
		os.Exit(1)
	}

	connCtx, cancelConn := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelConn()

	pool, err := pgxpool.New(connCtx, dbURL)
	if err != nil {
		log.Error("connect db", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := pool.Ping(connCtx); err != nil {
		log.Error("ping db", "err", err)
		os.Exit(1)
	}

	signer := auth.NewSigner(jwtSecret, 24*time.Hour)
	api := handlers.New(pool, signer, log)

	// The accrual scheduler (design Q5c) runs independently of HTTP routing:
	// api.Scheduler wires the manual POST /api/leave_balances/accrue handler
	// to the same AccrueCompany entry point the daily ticker below uses.
	sched := scheduler.New(pool, api.Queries, log, accrualEnabled, accrualHour)
	api.Scheduler = sched

	// signal.NotifyContext + srv.Shutdown + a scheduler "done" channel: the
	// scheduler goroutine signals done only after Run returns, so shutdown
	// waits for an in-flight accrual run to finish before the process exits
	// (design Q5c).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	schedDone := make(chan struct{})
	go func() {
		sched.Run(ctx)
		close(schedDone)
	}()

	addr := ":8080"
	srv := &http.Server{Addr: addr, Handler: api.Routes(signer)}

	serveErr := make(chan error, 1)
	go func() {
		log.Info("RRHH-Go API listening", "addr", addr)
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		log.Info("shutdown signal received")
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server stopped", "err", err)
			os.Exit(1)
		}
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("server shutdown", "err", err)
	}

	<-schedDone
	log.Info("shutdown complete")
}

// parseBoolEnv reads an optional boolean env var, returning def when unset.
func parseBoolEnv(key string, def bool) (bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	return strconv.ParseBool(v)
}

// parseIntEnv reads an optional integer env var, returning def when unset.
func parseIntEnv(key string, def int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	return strconv.Atoi(v)
}
