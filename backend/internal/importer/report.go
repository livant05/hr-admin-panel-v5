package importer

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// TableReport is the per-table outcome. In a dry run Inserted means "would
// insert".
type TableReport struct {
	Name     string
	Missing  bool // no <table>.json in the export directory
	Read     int
	Inserted int
	Present  int // id already in the database, left untouched
	Invalid  int
	Dropped  []string // JSON keys with no live (or only a generated) column
}

// InvalidRow is one row the importer refused, with the reason.
type InvalidRow struct {
	Table  string
	ID     string
	Reason string
}

// Report is the full outcome of a run.
type Report struct {
	DryRun  bool
	Tables  []TableReport
	Invalid []InvalidRow
	Ignored []string // *.json files in the directory that map to no table
	// UsersCreated counts users that got (or, in a dry run, would get) a
	// temporary password.
	UsersCreated int
	// CommitWarning is set when the commit reported an error but the data
	// was in fact committed.
	CommitWarning string
}

// Table returns the report of the named table, or nil.
func (r *Report) Table(name string) *TableReport {
	for i := range r.Tables {
		if r.Tables[i].Name == name {
			return &r.Tables[i]
		}
	}
	return nil
}

// Write prints the report as a readable table. It never prints row values
// other than the ids of invalid rows.
func (r *Report) Write(w io.Writer) {
	mode := "import"
	if r.DryRun {
		mode = "dry run (nothing written)"
	}
	fmt.Fprintf(w, "Supabase import: %s\n\n", mode)

	insertedHdr := "inserted"
	if r.DryRun {
		insertedHdr = "would insert"
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "table\tread\t%s\talready present\tinvalid\tdropped keys\n", insertedHdr)
	for _, t := range r.Tables {
		if t.Missing {
			fmt.Fprintf(tw, "%s\t-\t-\t-\t-\t(no file, skipped)\n", t.Name)
			continue
		}
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%s\n", t.Name, t.Read, t.Inserted, t.Present, t.Invalid, strings.Join(t.Dropped, ","))
	}
	tw.Flush()

	if len(r.Ignored) > 0 {
		fmt.Fprintf(w, "\nIgnored files (not a known table): %s\n", strings.Join(r.Ignored, ", "))
	}
	if r.UsersCreated > 0 {
		verb := "created"
		if r.DryRun {
			verb = "would be created"
		}
		fmt.Fprintf(w, "\nUsers %s with a temporary password: %d\n", verb, r.UsersCreated)
	}
	if r.CommitWarning != "" {
		fmt.Fprintf(w, "\nWARNING: %s\n", r.CommitWarning)
	}
	if len(r.Invalid) > 0 {
		fmt.Fprintf(w, "\nInvalid rows (%d):\n", len(r.Invalid))
		tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "table\tid\treason")
		for _, iv := range r.Invalid {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", iv.Table, iv.ID, iv.Reason)
		}
		tw.Flush()
	}
}
