package store

import (
	"strings"
	"testing"

	"go.uber.org/zap"
)

// Both reads of the day's totals search an index; neither scans a table.
func TestTodayQueries_UseIndexes(t *testing.T) {
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
	for _, c := range []struct{ name, sql, want string }{
		{"stubs", exchangeStubsSinceSQL, "SEARCH exchanges USING INDEX exchanges_started_at"},
		{"call events", callEventsSQL, "SEARCH events USING PRIMARY KEY"},
	} {
		rows, err := r.db.Query("EXPLAIN QUERY PLAN "+c.sql, 0)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		_ = rows.Close()
		t.Logf("%s: %q", c.name, plan)
		found := false
		for _, line := range plan {
			if strings.HasPrefix(line, "SCAN events") || strings.HasPrefix(line, "SCAN exchanges") {
				t.Fatalf("%s scans a table: %q", c.name, plan)
			}
			found = found || strings.Contains(line, c.want)
		}
		if !found {
			t.Fatalf("%s plan %q lacks %q", c.name, plan, c.want)
		}
	}
}
