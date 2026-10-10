package handlers

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestPgErrCode(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"non-pg", errors.New("boom"), ""},
		{"pg 23503", &pgconn.PgError{Code: "23503"}, "23503"},
		{"wrapped pg", errors.Join(errors.New("ctx"), &pgconn.PgError{Code: "23505"}), "23505"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pgErrCode(tc.err); got != tc.want {
				t.Fatalf("pgErrCode = %q, want %q", got, tc.want)
			}
		})
	}
}
