// Command import loads a Supabase data export (one <table>.json per table)
// into the Go database, preserving ids. See backend/README.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/importer"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}

// run returns the process exit code: 0 success, 1 import failure, 2 usage.
func run(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "directory with one <table>.json per table (required)")
	dbURL := fs.String("db-url", "", "database URL (default: env DB_URL)")
	dryRun := fs.Bool("dry-run", false, "validate and report, then roll back; writes nothing")
	skipInvalid := fs.Bool("skip-invalid", false, "skip invalid rows and list them instead of aborting")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *dir == "" {
		fmt.Fprintln(stderr, "import: --dir is required")
		return 2
	}
	if *dbURL == "" {
		*dbURL = getenv("DB_URL")
	}
	if *dbURL == "" {
		fmt.Fprintln(stderr, "import: --db-url or DB_URL is required")
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	connCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(connCtx, *dbURL)
	if err != nil {
		fmt.Fprintf(stderr, "import: connect db: %v\n", err)
		return 1
	}
	defer pool.Close()
	if err := pool.Ping(connCtx); err != nil {
		fmt.Fprintf(stderr, "import: ping db: %v\n", err)
		return 1
	}

	rep, err := importer.Run(ctx, pool, importer.Options{
		Dir: *dir, DryRun: *dryRun, SkipInvalid: *skipInvalid,
	})
	if rep != nil {
		rep.Write(stdout)
	}
	if err != nil {
		if errors.Is(err, importer.ErrInvalidRows) {
			fmt.Fprintf(stderr, "\nimport failed: %v (use --skip-invalid to skip them)\n", err)
		} else {
			fmt.Fprintf(stderr, "\nimport failed: %v\n", err)
		}
		return 1
	}
	return 0
}
