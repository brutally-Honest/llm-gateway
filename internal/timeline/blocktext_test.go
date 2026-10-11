package timeline

import "testing"

func TestBlockText_TextFieldElseRawJSON(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"type":"text","text":"hi"}`, "hi"},
		{`{"type":"text","text":""}`, ""},
		{`{"type":"other","x":1}`, `{"type":"other","x":1}`},
		{`not json`, `not json`},
	}
	for _, c := range cases {
		if got := blockText([]byte(c.in)); got != c.want {
			t.Errorf("blockText(%s) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := blockText(nil); got != NotStored {
		t.Errorf("nil = %q", got)
	}
}
