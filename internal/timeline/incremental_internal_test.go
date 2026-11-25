package timeline

import (
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/store"
)

// D16: rowsEqual compares every field of an exchange row. The test changes each field in
// turn, by reflection, so a field added to the row without a comparison fails here.
func TestRowsEqual_ComparesEveryField(t *testing.T) {
	ttfb := int64(5)
	mk := func() store.ExchangeRow {
		return store.ExchangeRow{RequestID: "a", RequestHeaders: http.Header{"K": {"v"}}, ResponseHeaders: http.Header{"R": {"w"}}, TTFBNS: &ttfb}
	}
	a, b := mk(), mk()
	if !rowsEqual(&a, &b) {
		t.Fatal("equal rows differ")
	}
	typ := reflect.TypeOf(a)
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		changed := mk()
		v := reflect.ValueOf(&changed).Elem().Field(i)
		switch v.Kind() {
		case reflect.String:
			v.SetString(v.String() + "x")
		case reflect.Int, reflect.Int64:
			v.SetInt(v.Int() + 1)
		case reflect.Bool:
			v.SetBool(!v.Bool())
		case reflect.Pointer: // TTFBNS: another value, then none
			other := int64(6)
			v.Set(reflect.ValueOf(&other))
			if rowsEqual(&a, &changed) {
				t.Errorf("%s: another value is equal", f.Name)
			}
			v.Set(reflect.Zero(v.Type()))
		case reflect.Map: // http.Header
			h := http.Header{"K": {"other"}}
			v.Set(reflect.ValueOf(h))
		default:
			t.Fatalf("%s: kind %s is not handled by this test; compare it in rowsEqual and add it here", f.Name, v.Kind())
		}
		if rowsEqual(&a, &changed) {
			t.Errorf("a change in %s is not seen", f.Name)
		}
	}
	// a header with a different value count, and TTFB by value
	c := mk()
	c.RequestHeaders = http.Header{"K": {"v", "v"}}
	if rowsEqual(&a, &c) {
		t.Error("a header with more values is equal")
	}
	d := mk()
	other := int64(5)
	d.TTFBNS = &other
	if !rowsEqual(&a, &d) {
		t.Error("equal TTFB values behind two pointers differ")
	}
}

// D15: the comparison of a hook's input with a call's is memoised when both are stored, and
// never when a read fails or a block is not stored.
func TestInputDiff_Memo(t *testing.T) {
	reads := 0
	stored := map[string]string{"h": `{"a":1}`, "c": `{"a": 1}`, "d": `{"a":2}`}
	read := func(hash string) ([]byte, error) {
		reads++
		if hash == "bad" {
			return nil, errors.New("disk")
		}
		if s, ok := stored[hash]; ok {
			return []byte(s), nil
		}
		return nil, nil
	}
	m := NewMemo()
	opts := Options{}
	d, err := inputDiff(read, m, "h", "d", opts)
	if err != nil || !d.differs || d.hookInput != `{"a":1}` {
		t.Fatalf("diff = %+v, %v", d, err)
	}
	n := reads
	if d2, _ := inputDiff(read, m, "h", "d", opts); d2 != d || reads != n {
		t.Fatalf("not memoised: reads %d -> %d", n, reads)
	}
	if d, _ := inputDiff(read, m, "h", "c", opts); d.differs {
		t.Fatalf("the same JSON written two ways differs: %+v", d)
	}
	// another cut is another key
	n = reads
	if _, _ = inputDiff(read, m, "h", "d", Options{Cut: 3}); reads == n {
		t.Fatal("a different cut reused the memo")
	}
	size := m.Size()
	for _, pair := range [][2]string{{"bad", "c"}, {"h", "bad"}, {"missing", "c"}, {"h", "missing"}} {
		_, err := inputDiff(read, m, pair[0], pair[1], opts)
		if (pair[0] == "bad" || pair[1] == "bad") != (err != nil) {
			t.Fatalf("%v: err = %v", pair, err)
		}
	}
	if m.Size() != size {
		t.Fatal("a failed or not-stored read was memoised")
	}
}
