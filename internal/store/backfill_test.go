package store_test

import (
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/store"
)

func TestSetSessionKeys_OnlyFillsNull(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	keyed := exchange("keyed", []byte("a"), []byte("b"))
	keyed.SessionKey = "original"
	if err := s.SaveExchange(t.Context(), keyed); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveExchange(t.Context(), exchange("bare", []byte("a"), []byte("b"))); err != nil {
		t.Fatal(err)
	}
	batch, err := s.UnkeyedBatch(t.Context(), 0, 10)
	if err != nil || len(batch) != 1 || batch[0].RequestID != "bare" {
		t.Fatalf("UnkeyedBatch = %+v, err %v, want only bare", batch, err)
	}
	n, err := s.SetSessionKeys(t.Context(), []store.KeyAssignment{
		{RequestID: "keyed", Key: "other"}, {RequestID: "bare", Key: "new"}, {RequestID: "missing", Key: "x"}})
	if err != nil || n != 1 {
		t.Fatalf("SetSessionKeys = %d, %v, want 1", n, err)
	}
	r := openReader(t, dir)
	for id, want := range map[string]string{"keyed": "original", "bare": "new"} {
		row, err := r.Exchange(id)
		if err != nil || row.SessionKey != want {
			t.Fatalf("%s key = %q, %v, want %q", id, row.SessionKey, err, want)
		}
	}
	n, err = s.SetSessionKeys(t.Context(), []store.KeyAssignment{{RequestID: "bare", Key: "again"}})
	if err != nil || n != 0 {
		t.Fatalf("second SetSessionKeys = %d, %v, want 0", n, err)
	}
}
