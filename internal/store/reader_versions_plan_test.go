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

// The unassigned-connection check walks the effects primary key from the given id on.
func TestUnassignedConnAfter_PlanUsesPrimaryKey(t *testing.T) {
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
	rows, err := r.db.Query(`EXPLAIN QUERY PLAN SELECT EXISTS (SELECT 1 FROM effects
		WHERE id > ? AND session_key IS NULL AND kind = 'net_conn' AND at >= ? AND at <= ?)`, 1, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	found := false
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		t.Log(detail)
		if strings.Contains(detail, "SEARCH effects USING INTEGER PRIMARY KEY (rowid>?)") {
			found = true
		}
	}
	if !found {
		t.Error("the check does not search the effects primary key by id > ?")
	}
}

// The page-and-total queries run the list aggregate once: each of exchanges and hook_events
// is scanned or searched a single time in the plan, as in the plain list query.
func TestSessionPageTotal_QueryPlanAggregatesOnce(t *testing.T) {
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
	count := func(plan []string, table string) int {
		n := 0
		for _, l := range plan {
			if (strings.HasPrefix(l, "SCAN "+table) || strings.HasPrefix(l, "SEARCH "+table)) && !strings.Contains(l, "EXISTS") {
				n++
			}
		}
		return n
	}
	base := planRows(t, r, sessionSelect+sessionOrder+` LIMIT 5`, 0)
	for name, q := range map[string]string{
		"after": sessionWindow + ` AND (last < 1 OR (last = 1 AND (principal_id, client, session_key) > ('a', 'b', 'c')))` + sessionOrder + ` LIMIT 5`,
		"at":    sessionWindow + sessionOrder + ` LIMIT 5 OFFSET 2`,
	} {
		plan := planRows(t, r, q, 0)
		t.Logf("%s: %q", name, plan)
		if count(plan, "exchanges") != count(base, "exchanges") || count(plan, "hook_events") != count(base, "hook_events") {
			t.Errorf("%s plan scans differ from the plain list query: %q vs %q", name, plan, base)
		}
	}
}
