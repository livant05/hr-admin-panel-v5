package handlers_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/livant05/rrhh-go/internal/testutil"
)

// seedIngestData writes one rejection counter and one unknown pin for the
// serial, the two tables ADMS ingestion fills for a device (ING-6, ING-9).
func seedIngestData(t *testing.T, pool *pgxpool.Pool, serial string) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO zkteco_ingest_rejections (serial_number, reason, count, last_pin) VALUES ($1, 'device_unclaimed', 4, '8-123-456')`,
		serial); err != nil {
		t.Fatalf("seed rejection: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO zkteco_unknown_pins (pin, serial_number, attempts, last_seen) VALUES ($1, $2, 2, $3)`,
		"CLAIM-"+uuid.NewString()[:8], serial, time.Now()); err != nil {
		t.Fatalf("seed unknown pin: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM zkteco_ingest_rejections WHERE serial_number = $1`, serial)
		_, _ = pool.Exec(context.Background(), `DELETE FROM zkteco_unknown_pins WHERE serial_number = $1`, serial)
	})
}

// ingestData returns how many rejection rows and unknown pins exist for serial.
func ingestData(t *testing.T, pool *pgxpool.Pool, serial string) (rejections, pins int) {
	t.Helper()
	ctx := context.Background()
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM zkteco_ingest_rejections WHERE serial_number = $1`, serial).Scan(&rejections); err != nil {
		t.Fatalf("count rejections: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM zkteco_unknown_pins WHERE serial_number = $1`, serial).Scan(&pins); err != nil {
		t.Fatalf("count pins: %v", err)
	}
	return rejections, pins
}

// CO-S5a-2: a device collects counters and unknown pins while unclaimed. The
// new owner must not inherit them (they hold punch data of whoever stood in
// front of the device before the claim).
func TestZktecoDevices_ClaimClearsEarlierIngestData(t *testing.T) {
	pool := testutil.Pool(t)
	h, signer := testutil.Server(t, pool)

	a := testutil.Company(t, pool, "ZkClaimCleanA")
	b := testutil.Company(t, pool, "ZkClaimCleanB")
	ta := a.Token(t, signer)

	t.Run("a successful claim deletes that serial's rejections and unknown pins only", func(t *testing.T) {
		dev := seedZkDevice(t, pool, zkSeed{})
		other := seedZkDevice(t, pool, zkSeed{})
		seedIngestData(t, pool, dev.SerialNumber)
		seedIngestData(t, pool, other.SerialNumber)
		if r, p := ingestData(t, pool, dev.SerialNumber); r != 1 || p != 1 {
			t.Fatalf("precondition: rejections=%d pins=%d, want 1 and 1", r, p)
		}

		if rec := testutil.Do(t, h, http.MethodPost, claimPath(dev.ID), ta, nil); rec.Code != http.StatusOK {
			t.Fatalf("claim got %d (%s)", rec.Code, rec.Body.String())
		}

		if r, p := ingestData(t, pool, dev.SerialNumber); r != 0 || p != 0 {
			t.Fatalf("claimed serial kept rejections=%d pins=%d, want 0 and 0", r, p)
		}
		if r, p := ingestData(t, pool, other.SerialNumber); r != 1 || p != 1 {
			t.Fatalf("another serial lost data: rejections=%d pins=%d, want 1 and 1", r, p)
		}
		// The claimed pin must not resurface through the new owner's list.
		rec := testutil.Do(t, h, http.MethodGet, zkUnknownPinsPath+"?_limit=1000", ta, nil)
		for _, row := range testutil.DecodeRows[unknownPinRow](t, rec) {
			if row.SerialNumber != nil && *row.SerialNumber == dev.SerialNumber {
				t.Fatalf("new owner inherited pin %q", row.Pin)
			}
		}
	})

	t.Run("a claim that fails deletes nothing", func(t *testing.T) {
		owned := seedZkDevice(t, pool, zkSeed{companyID: &b.CompanyID})
		seedIngestData(t, pool, owned.SerialNumber)

		// A tries to claim a device B already owns, a missing id, and a
		// non-admin tries a free one: every attempt fails.
		if rec := testutil.Do(t, h, http.MethodPost, claimPath(owned.ID), ta, nil); rec.Code != http.StatusNotFound {
			t.Fatalf("claim of a claimed device got %d, want 404", rec.Code)
		}
		if rec := testutil.Do(t, h, http.MethodPost, claimPath(uuid.NewString()), ta, nil); rec.Code != http.StatusNotFound {
			t.Fatalf("claim of a missing id got %d, want 404", rec.Code)
		}
		free := seedZkDevice(t, pool, zkSeed{})
		seedIngestData(t, pool, free.SerialNumber)
		if rec := testutil.Do(t, h, http.MethodPost, claimPath(free.ID), a.TokenAs(t, pool, signer, "empleado"), nil); rec.Code != http.StatusForbidden {
			t.Fatalf("non-admin claim got %d, want 403", rec.Code)
		}
		if rec := testutil.Do(t, h, http.MethodPost, claimPath(free.ID), ta, map[string]any{"company_id": b.CompanyID}); rec.Code != http.StatusBadRequest {
			t.Fatalf("claim with a company in the body got %d, want 400", rec.Code)
		}

		if r, p := ingestData(t, pool, owned.SerialNumber); r != 1 || p != 1 {
			t.Fatalf("owned device lost data to a failed claim: rejections=%d pins=%d", r, p)
		}
		if r, p := ingestData(t, pool, free.SerialNumber); r != 1 || p != 1 {
			t.Fatalf("unclaimed device lost data to a rejected claim: rejections=%d pins=%d", r, p)
		}
	})
}
