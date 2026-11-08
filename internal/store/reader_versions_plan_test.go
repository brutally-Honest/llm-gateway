package store

import (
	"strings"
	"testing"

	"go.uber.org/zap"
)

// planRows runs EXPLAIN QUERY PLAN on q with n requested sessions and returns the details.
func planRows(t *testing.T, r *Reader, q string, n int) []string {
	t.Helper()
	args := make([]any, 3*n)
	for i := range args {
		args[i] = "x"
	}
	rows, err := r.db.Query("EXPLAIN QUERY PLAN "+q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		out = append(out, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSessionVersions_QueryPlansUseSessionIndex(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	r, err := OpenReader(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	cases := []struct{ name, tail, table, index string }{
		{"exchanges", exchangeVersionTail, "exchanges", "exchanges_session"},
		{"hook_events", hookVersionTail, "hook_events", "hook_events_session"},
	}
	for _, c := range cases {
		for _, n := range []int{1, 50} {
			plan := planRows(t, r, versionSQL(c.tail, n), n)
			t.Logf("%s n=%d: %q", c.name, n, plan)
			found := false
			for _, line := range plan {
				if strings.Contains(line, "SCAN l") || strings.Contains(line, "CONSTANT ROW") || strings.Contains(line, "SCAN CONSTANT") {
					continue
				}
				if strings.Contains(line, "exchanges_principal") {
					t.Fatalf("%s n=%d: uses exchanges_principal: %q", c.name, n, line)
				}
				if strings.HasPrefix(line, "SCAN") && !strings.Contains(line, "SCAN l") &&
					(strings.Contains(line, c.table) || strings.Contains(line, " e") || strings.Contains(line, " h")) {
					t.Fatalf("%s n=%d: scans the target: %q", c.name, n, line)
				}
				if strings.HasPrefix(line, "SEARCH") {
					found = true
					if !strings.Contains(line, "USING") || !strings.Contains(line, c.index) ||
						!strings.Contains(line, "principal_id=?") || !strings.Contains(line, "client=?") ||
						!strings.Contains(line, "session_key=?") {
						t.Fatalf("%s n=%d: bad search line %q (plan %q)", c.name, n, line, plan)
					}
				}
			}
			if !found {
				t.Fatalf("%s n=%d: no SEARCH line in %q", c.name, n, plan)
			}
		}
	}
}
