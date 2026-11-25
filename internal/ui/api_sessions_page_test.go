package ui

import (
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

type sessPage struct {
	Items     []map[string]any `json:"items"`
	NextAfter *string          `json:"next_after"`
	Total     int              `json:"total"`
	Scanned   int              `json:"scanned"`
	Checking  int              `json:"checking"`
}

// sess is the page once its rows' facts are built (see page); sessNow is the first answer.
func (e *apiEnv) sess(query string) sessPage {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		p := e.sessNow(query)
		pending := p.Checking > 0
		for _, it := range p.Items {
			pending = pending || it["pending"] == true
		}
		if !pending {
			return p
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("%s: rows still pending after 10 s", query)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (e *apiEnv) sessNow(query string) sessPage {
	e.t.Helper()
	rec := e.get("/api/sessions" + query)
	if rec.Code != 200 {
		e.t.Fatalf("%s = %d %s", query, rec.Code, rec.Body)
	}
	var p sessPage
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		e.t.Fatal(err)
	}
	if p.Items == nil {
		e.t.Fatalf("%s: items is null", query)
	}
	return p
}

func (p sessPage) keys() []string {
	out := []string{}
	for _, it := range p.Items {
		out = append(out, it["session_key"].(string))
	}
	return out
}

// walk follows next_after until null and returns every key and the number of requests.
func (e *apiEnv) walk(query string) ([]string, int) {
	e.t.Helper()
	var all []string
	p := e.sess(query)
	n := 1
	for {
		all = append(all, p.keys()...)
		if p.NextAfter == nil {
			return all, n
		}
		if n > 100 {
			e.t.Fatal("walk does not end")
		}
		p = e.sess(query + "&after=" + url.QueryEscape(*p.NextAfter))
		n++
	}
}

func (e *apiEnv) seed(n int) {
	for i := range n {
		e.save(fmt.Sprintf("r%d", i), fmt.Sprintf("s%03d", i), "cc", i+1)
	}
}

func TestSessionsPage_Sizes(t *testing.T) {
	for _, tc := range []struct {
		name         string
		n            int
		first, total int
		second       int
		more         bool
	}{
		{"empty", 0, 0, 0, 0, false},
		{"exactly 50", 50, 50, 50, 0, false},
		{"51", 51, 50, 51, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newAPIEnv(t)
			e.seed(tc.n)
			p := e.sess("")
			if len(p.Items) != tc.first || (p.NextAfter != nil) != tc.more || p.Total != tc.total || p.Scanned != tc.first {
				t.Fatalf("page 1 = %d items next %v total %d scanned %d", len(p.Items), p.NextAfter, p.Total, p.Scanned)
			}
			if !tc.more {
				return
			}
			p2 := e.sess("?after=" + url.QueryEscape(*p.NextAfter))
			if len(p2.Items) != tc.second || p2.NextAfter != nil || p2.Total != tc.total {
				t.Fatalf("page 2 = %v next %v total %d", p2.keys(), p2.NextAfter, p2.Total)
			}
			seen := map[string]bool{}
			for _, k := range append(p.keys(), p2.keys()...) {
				if seen[k] {
					t.Fatalf("%s repeated", k)
				}
				seen[k] = true
			}
			if len(seen) != tc.n {
				t.Fatalf("saw %d of %d", len(seen), tc.n)
			}
		})
	}
}

func TestSessionsPage_AfterPastEnd(t *testing.T) {
	e := newAPIEnv(t)
	e.seed(3)
	last := e.sess("?limit=1&after=" + url.QueryEscape(*e.sess("?limit=2").NextAfter))
	if len(last.Items) != 1 || last.NextAfter != nil {
		t.Fatalf("last = %v %v", last.keys(), last.NextAfter)
	}
	// an after token older than every row has nothing after it
	tok := encodeAfter(afterToken{List: listSessions, At: 0, Principal: core.PrincipalLocal, Client: "cc", Key: "zzz"})
	for _, q := range []string{"", "&kind=active", "&q=s"} {
		p := e.sess("?after=" + url.QueryEscape(tok) + q)
		if len(p.Items) != 0 || p.NextAfter != nil || p.Total != 3 {
			t.Fatalf("past end %q = %v next %v total %d", q, p.keys(), p.NextAfter, p.Total)
		}
	}
}

func TestSessionsPage_FilterAndSearchTogether(t *testing.T) {
	e := newAPIEnv(t)
	// active: recent (now is base+100s); old: more than 5 minutes before now
	e.save("a", "alpha-new", "cc", 10)
	e.save("b", "beta-new", "cc", 11)
	e.save("c", "alpha-old", "cc", -1000)
	e.save("d", "beta-old", "cc", -1001)
	for q, want := range map[string]string{
		"?kind=active":            "[beta-new alpha-new]",
		"?q=ALPHA":                "[alpha-new alpha-old]",
		"?q=%20alpha%20":          "[alpha-new alpha-old]",
		"?kind=active&q=alpha":    "[alpha-new]",
		"?kind=hooks&q=alpha":     "[]",
		"?kind=all&q=":            "[beta-new alpha-new alpha-old beta-old]",
		"?kind=active&q=nomatch":  "[]",
		"?kind=active&unknown=zz": "[beta-new alpha-new]",
	} {
		p := e.sess(q)
		if fmt.Sprint(p.keys()) != want || p.Total != 4 {
			t.Errorf("%s = %v total %d, want %s", q, p.keys(), p.Total, want)
		}
	}
	// first value wins for a repeated parameter
	if p := e.sess("?kind=active&kind=bogus"); len(p.Items) != 2 {
		t.Errorf("repeated kind = %v", p.keys())
	}
}

func TestSessionsPage_ModelSearchAndHooksKind(t *testing.T) {
	e := newAPIEnv(t)
	e.save("a", "withmodel", "cc", 1)
	e.parse("a", "m-special", "")
	e.save("b", "plain", "cc", 2)
	e.hook("hookonly", core.HarnessEvent{Phase: core.PhaseNotice}, 3)
	if got := e.sess("?q=SPECIAL").keys(); fmt.Sprint(got) != "[withmodel]" {
		t.Errorf("model search = %v", got)
	}
	if got := e.sess("?kind=hooks").keys(); fmt.Sprint(got) != "[hookonly]" {
		t.Errorf("hooks = %v", got)
	}
}

func TestSessionMatches_Kinds(t *testing.T) {
	look := sessionSummary{List: &listJSON{NeedsLook: 2}}
	for _, tc := range []struct {
		name string
		s    sessionSummary
		kind string
		want bool
	}{
		{"look yes", look, "look", true},
		{"look zero", sessionSummary{List: &listJSON{}}, "look", false},
		{"look nil list", sessionSummary{}, "look", false},
		{"active", sessionSummary{Active: true}, "active", true},
		{"not active", sessionSummary{}, "active", false},
		{"hooks only", sessionSummary{HooksOnly: true}, "hooks", true},
		{"hooks no calls", sessionSummary{HasHooks: true}, "hooks", true},
		{"hooks with calls", sessionSummary{HasHooks: true, HasCalls: true}, "hooks", false},
		{"all", sessionSummary{}, "all", true},
	} {
		if got := sessionMatches(tc.s, tc.kind, ""); got != tc.want {
			t.Errorf("%s = %v", tc.name, got)
		}
	}
	s := sessionSummary{Title: "Fix X", List: &listJSON{Folder: "proj", FolderFull: "/home/proj"}, Models: []string{"m-1"}}
	for _, q := range []string{"fix x", "proj", "/home/proj", "m-1"} {
		if !sessionMatches(s, "all", q) {
			t.Errorf("q %q did not match", q)
		}
	}
	if sessionMatches(s, "all", "nope") {
		t.Error("q nope matched")
	}
}

func TestSessionsPage_NewRowsBetweenPages(t *testing.T) {
	e := newAPIEnv(t)
	e.seed(5)
	p := e.sess("?limit=2") // s004 s003
	seen := p.keys()
	e.save("n1", "new1", "cc", 90)
	e.save("n2", "new2", "cc", 91)
	for p.NextAfter != nil {
		p = e.sess("?limit=2&after=" + url.QueryEscape(*p.NextAfter))
		seen = append(seen, p.keys()...)
	}
	if fmt.Sprint(seen) != "[s004 s003 s002 s001 s000]" {
		t.Fatalf("walk = %v", seen)
	}
	// same through a filtered walk
	e2 := newAPIEnv(t)
	e2.seed(5)
	p = e2.sess("?limit=2&kind=active")
	seen = p.keys()
	e2.save("n1", "new1", "cc", 90)
	for p.NextAfter != nil {
		p = e2.sess("?limit=2&kind=active&after=" + url.QueryEscape(*p.NextAfter))
		seen = append(seen, p.keys()...)
	}
	if fmt.Sprint(seen) != "[s004 s003 s002 s001 s000]" {
		t.Fatalf("filtered walk = %v", seen)
	}
}

func TestSessionsPage_FilteredScanNoSkipNoRepeat(t *testing.T) {
	e := newAPIEnv(t)
	e.seed(23)
	// a single match among 23 rows is found whatever the limit, with no row skipped or repeated
	for _, limit := range []int{1, 2, 3, 6} {
		var got []string
		p := e.sess(fmt.Sprintf("?limit=%d&q=s000", limit))
		got = append(got, p.keys()...)
		for p.NextAfter != nil {
			p = e.sess(fmt.Sprintf("?limit=%d&q=s000&after=%s", limit, url.QueryEscape(*p.NextAfter)))
			got = append(got, p.keys()...)
		}
		if fmt.Sprint(got) != "[s000]" {
			t.Errorf("limit %d walk = %v", limit, got)
		}
	}
	// stops at the limit and reports the position of the last tested row
	p := e.sess("?limit=2&q=s01")
	if fmt.Sprint(p.keys()) != "[s019 s018]" || p.NextAfter == nil || p.Scanned != 5 {
		t.Fatalf("q=s01 = %v next %v scanned %d", p.keys(), p.NextAfter, p.Scanned)
	}
	all, _ := e.walk("?limit=2&q=s01")
	if fmt.Sprint(all) != "[s019 s018 s017 s016 s015 s014 s013 s012 s011 s010]" {
		t.Fatalf("walk = %v", all)
	}
}

func TestSessionsPage_ScanBudgetStopsEarly(t *testing.T) {
	old := scanBudget
	scanBudget = -time.Second // already spent: only the first batch of each request runs
	t.Cleanup(func() { scanBudget = old })
	e := newAPIEnv(t)
	e.seed(120) // newest first: s119 ... s000; the first batch tests s119..s069
	p := e.sess("?q=s1")
	if p.Scanned != defaultListLimit+1 || p.NextAfter == nil || len(p.Items) != 20 || p.Items[19]["session_key"] != "s100" {
		t.Fatalf("first = %d items %v next %v scanned %d", len(p.Items), p.keys(), p.NextAfter, p.Scanned)
	}
	all, n := e.walk("?q=s1")
	if n < 3 || len(all) != 20 {
		t.Fatalf("walk = %d keys in %d requests", len(all), n)
	}
	seen := map[string]bool{}
	for _, k := range all {
		if seen[k] {
			t.Fatalf("%s repeated", k)
		}
		seen[k] = true
	}
	for i := 100; i < 120; i++ {
		if !seen[fmt.Sprintf("s%03d", i)] {
			t.Fatalf("s%03d skipped", i)
		}
	}
	// a budget stop with no untested row left gives a null after token
	e2 := newAPIEnv(t)
	e2.seed(51)
	if p := e2.sess("?q=nomatch"); p.NextAfter != nil || p.Scanned != 51 {
		t.Fatalf("end = next %v scanned %d", p.NextAfter, p.Scanned)
	}
}

func TestSessionsPage_ScanBudgetCountsLastBatch(t *testing.T) {
	// a fake clock that moves one second per reading: every batch then lasts one second
	var ticks atomic.Int64
	oldNow, oldBudget := scanNow, scanBudget
	scanNow = func() time.Time { return time.Unix(ticks.Add(1), 0) }
	// the first check reads 3 s elapsed plus a 1 s batch: over a 3.5 s budget, though elapsed alone is not
	scanBudget = 3500 * time.Millisecond
	t.Cleanup(func() { scanNow, scanBudget = oldNow, oldBudget })
	e := newAPIEnv(t)
	e.seed(120)
	p := e.sess("?q=s1")
	if p.Scanned != defaultListLimit+1 || p.NextAfter == nil {
		t.Fatalf("first = scanned %d next %v", p.Scanned, p.NextAfter)
	}
	all, n := e.walk("?q=s1")
	if n < 3 || len(all) != 20 {
		t.Fatalf("walk = %d keys in %d requests", len(all), n)
	}
	seen := map[string]bool{}
	for _, k := range all {
		if seen[k] {
			t.Fatalf("%s repeated", k)
		}
		seen[k] = true
	}
	for i := 100; i < 120; i++ {
		if !seen[fmt.Sprintf("s%03d", i)] {
			t.Fatalf("s%03d skipped", i)
		}
	}
}

func TestSessionMatches_FieldsAreSeparate(t *testing.T) {
	s := sessionSummary{Title: "xa", Client: "bx"}
	if sessionMatches(s, "all", "a\nb") {
		t.Fatal("q with a newline matched across the title and client fields")
	}
	if !sessionMatches(s, "all", "xa") || !sessionMatches(s, "all", "bx") {
		t.Fatal("single-field match lost")
	}
}

func TestSessionsPage_ScanCap(t *testing.T) {
	oldBudget := scanBudget
	scanBudget = time.Hour // only the row cap stops this test
	// 500 cold rows are too slow under -race, so the test lowers the cap; the rule is the same
	oldCap := scanCap
	scanCap = 50
	t.Cleanup(func() { scanBudget, scanCap = oldBudget, oldCap })
	e := newAPIEnv(t)
	// 60 sessions; only the oldest matches, so the first request tests the cap and finds none
	for i := range 60 {
		e.save(fmt.Sprintf("r%d", i), fmt.Sprintf("s%03d", i), "cc", i+1)
	}
	p := e.sess("?q=s000")
	if len(p.Items) != 0 || p.NextAfter == nil || p.Scanned != scanCap || p.Total != 60 {
		t.Fatalf("capped = %v next %v scanned %d total %d", p.keys(), p.NextAfter, p.Scanned, p.Total)
	}
	p = e.sess("?q=s000&after=" + url.QueryEscape(*p.NextAfter))
	if fmt.Sprint(p.keys()) != "[s000]" || p.NextAfter != nil || p.Scanned != 10 {
		t.Fatalf("second = %v next %v scanned %d", p.keys(), p.NextAfter, p.Scanned)
	}
	// exactly scanCap rows and no match: nothing left, so next_after is null
	e2 := newAPIEnv(t)
	for i := range scanCap {
		e2.save(fmt.Sprintf("r%d", i), fmt.Sprintf("s%03d", i), "cc", i+1)
	}
	if p := e2.sess("?q=nomatch"); p.NextAfter != nil || p.Scanned != scanCap {
		t.Fatalf("exact cap = next %v scanned %d", p.NextAfter, p.Scanned)
	}
}

func TestSessionsPage_SearchIsLiteral(t *testing.T) {
	e := newAPIEnv(t)
	keys := []string{`a%b`, `a_b`, `a'b`, `a"b`, `a\b`, `axb`, `aab`}
	for i, k := range keys {
		e.save(fmt.Sprintf("r%d", i), k, "cc", i+1)
	}
	for _, k := range keys[:5] {
		p := e.sess("?q=" + url.QueryEscape(k))
		if fmt.Sprint(p.keys()) != fmt.Sprint([]string{k}) {
			t.Errorf("q=%s = %v", k, p.keys())
		}
	}
	for _, q := range []string{"%", "_"} {
		if got := e.sess("?q=" + url.QueryEscape(q)).keys(); len(got) != 1 {
			t.Errorf("q=%s = %v", q, got)
		}
	}
}

func TestSessionsPage_BadParams(t *testing.T) {
	e := newAPIEnv(t)
	e.seed(2)
	e.save("u1", "", "cc", 5)
	e.save("u2", "", "cc", 6)
	up := e.page("/api/ungrouped?limit=1")
	long := make([]byte, 201)
	for i := range long {
		long[i] = 'a'
	}
	for _, q := range []string{
		"limit=0", "limit=-1", "limit=abc", "after=garbage", "after=" + url.QueryEscape(*up.NextAfter),
		"kind=", "kind=bogus", "kind=ALL", "q=" + string(long), "q=%20" + string(long) + "%20x",
	} {
		wantEnvelope(t, e.get("/api/sessions?"+q), 400, "bad_request")
	}
	// 200 bytes after trimming is fine
	if rec := e.get("/api/sessions?q=%20" + string(long[:200]) + "%20"); rec.Code != 200 {
		t.Errorf("200-byte q = %d", rec.Code)
	}
}

func TestSessionsPage_LimitClampedTo50(t *testing.T) {
	e := newAPIEnv(t)
	e.seed(60)
	for _, q := range []string{"?limit=500", "?limit=51", "?limit=500&q=s"} {
		p := e.sess(q)
		if len(p.Items) != 50 || p.NextAfter == nil {
			t.Errorf("%s = %d items next %v", q, len(p.Items), p.NextAfter)
		}
	}
	if len(e.sess("?limit=7").Items) != 7 {
		t.Error("limit=7")
	}
	// /api/ungrouped keeps its own cap of 200
	for i := range 210 {
		e.save(fmt.Sprintf("u%d", i), "", "cc", i+1)
	}
	if p := e.page("/api/ungrouped?limit=500"); len(p.Items) != 200 {
		t.Errorf("ungrouped clamp = %d", len(p.Items))
	}
}

func TestSessionsPage_TotalIgnoresFilter(t *testing.T) {
	e := newAPIEnv(t)
	e.seed(4)
	p := e.sess("?q=s001")
	if p.Total != 4 || p.Scanned != 4 || len(p.Items) != 1 {
		t.Fatalf("total %d scanned %d items %d", p.Total, p.Scanned, len(p.Items))
	}
	p = e.sess("?q=nomatch")
	if p.Total != 4 || len(p.Items) != 0 || p.NextAfter != nil {
		t.Fatalf("nomatch total %d items %d next %v", p.Total, len(p.Items), p.NextAfter)
	}
}

func TestSessionsPage_SmallLimitFilteredPages(t *testing.T) {
	e := newAPIEnv(t)
	// 120 sessions; every 10th key contains "hit", the rest contain "miss"
	var want []string
	for i := range 120 {
		key := fmt.Sprintf("miss%03d", i)
		if i%10 == 0 {
			key = fmt.Sprintf("hit%03d", i)
			want = append([]string{key}, want...) // store order is newest first
		}
		e.save(fmt.Sprintf("r%d", i), key, "cc", i+1)
	}
	seen := map[string]bool{}
	var got []string
	p := e.sess("?limit=1&q=hit")
	for {
		for _, k := range p.keys() {
			if seen[k] {
				t.Fatalf("%s repeated", k)
			}
			seen[k] = true
			got = append(got, k)
		}
		if p.NextAfter == nil {
			break
		}
		p = e.sess("?limit=1&q=hit&after=" + url.QueryEscape(*p.NextAfter))
	}
	if len(got) != 12 || fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("matches = %v, want %v", got, want)
	}
}

func TestSessionsPage_OffsetPages(t *testing.T) {
	e := newAPIEnv(t)
	e.seed(120)
	first := e.sess("")
	if got := e.sess("?offset=0"); !reflect.DeepEqual(got.keys(), first.keys()) || got.NextAfter == nil || *got.NextAfter != *first.NextAfter {
		t.Fatalf("offset 0 = %v next %v, want the first page", got.keys(), got.NextAfter)
	}
	byAfter := e.sess("?after=" + url.QueryEscape(*first.NextAfter))
	p2 := e.sess("?offset=50")
	if !reflect.DeepEqual(p2.keys(), byAfter.keys()) || len(p2.Items) != 50 || p2.NextAfter == nil || p2.Scanned != 50 || p2.Total != 120 {
		t.Fatalf("offset 50 = %d rows next %v scanned %d total %d, want the after-token page", len(p2.Items), p2.NextAfter, p2.Scanned, p2.Total)
	}
	if want := *byAfter.NextAfter; *p2.NextAfter != want {
		t.Fatalf("offset 50 next_after differs from the token path")
	}
	p3 := e.sess("?offset=100")
	if len(p3.Items) != 20 || p3.NextAfter != nil || p3.Scanned != 20 {
		t.Fatalf("offset 100 = %d rows next %v", len(p3.Items), p3.NextAfter)
	}
	for _, off := range []string{"120", "500"} {
		p := e.sess("?offset=" + off)
		if len(p.Items) != 0 || p.NextAfter != nil || p.Total != 120 || p.Scanned != 0 {
			t.Fatalf("offset %s = %v next %v total %d", off, p.keys(), p.NextAfter, p.Total)
		}
	}
	var all []string
	for _, off := range []string{"0", "50", "100"} {
		all = append(all, e.sess("?offset="+off).keys()...)
	}
	want, _ := e.walk("?limit=50")
	if !reflect.DeepEqual(all, want) || len(all) != 120 {
		t.Fatalf("offsets 0,50,100 cover %d rows, want the %d of the after-token walk in order", len(all), len(want))
	}
	// a smaller limit moves the page by the offset, and next_after follows the last row
	p := e.sess("?offset=7&limit=3")
	if len(p.Items) != 3 || p.NextAfter == nil || !reflect.DeepEqual(p.keys(), want[7:10]) {
		t.Fatalf("offset 7 limit 3 = %v next %v", p.keys(), p.NextAfter)
	}
	if after := e.sess("?limit=3&after=" + url.QueryEscape(*p.NextAfter)); !reflect.DeepEqual(after.keys(), want[10:13]) {
		t.Fatalf("after offset page's token = %v", after.keys())
	}
}

func TestSessionsPage_OffsetKeepsTieOrder(t *testing.T) {
	e := newAPIEnv(t)
	for i := range 12 {
		e.save(fmt.Sprintf("r%d", i), fmt.Sprintf("s%02d", i), "cc", 1+i/4) // four sessions per last
	}
	want, _ := e.walk("?limit=5")
	if len(want) != 12 {
		t.Fatalf("walk saw %d", len(want))
	}
	var got []string
	for _, off := range []string{"0", "5", "10"} {
		got = append(got, e.sess("?limit=5&offset="+off).keys()...)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("offset order = %v, token order = %v", got, want)
	}
}

func TestSessionsPage_OffsetBadParams(t *testing.T) {
	e := newAPIEnv(t)
	e.seed(3)
	tok := url.QueryEscape(*e.sess("?limit=1").NextAfter)
	for _, q := range []string{
		"offset=-1", "offset=abc", "offset=1.5", "offset=", "offset=99999999999999999999",
		"offset=0&after=" + tok, "offset=1&after=" + tok,
		"offset=0&kind=active", "offset=1&kind=look", "offset=1&kind=hooks", "offset=0&q=x",
	} {
		wantEnvelope(t, e.get("/api/sessions?"+q), 400, "bad_request")
	}
	// kind=all and a q that trims to empty are the unfiltered list
	if p := e.sess("?offset=1&kind=all&q=%20"); len(p.Items) != 2 {
		t.Fatalf("offset with kind=all and blank q = %v", p.keys())
	}
}
