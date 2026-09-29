package core_test

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/brutally-honest/llm-gateway/internal/core"
)

func TestRedact_HeaderClone(t *testing.T) {
	in := http.Header{
		"Authorization": {"Bearer sk-sentinel"},
		"X-Secret":      {"sentinel"},
		"Content-Type":  {"application/json"},
	}
	want := in.Clone()

	out := core.RedactHeader(in, []string{"x-secret"})

	if !reflect.DeepEqual(in, want) {
		t.Fatalf("input header changed: got %v, want %v", in, want)
	}
	out["Content-Type"][0] = "changed"
	if in.Get("Content-Type") != "application/json" {
		t.Fatal("the redacted header shares value slices with the input")
	}
	if core.RedactHeader(nil, nil) != nil {
		t.Fatal("RedactHeader(nil) is not nil")
	}
}

func TestRedact_HeaderNamesKept(t *testing.T) {
	in := http.Header{
		"Authorization":       {"Bearer sk-1"},
		"Proxy-Authorization": {"Basic abc"},
		"Cookie":              {"a=1", "b=2"},
		"Set-Cookie":          {"s=1; HttpOnly"},
		"X-Test-Key":          {"sk-2"},
		"x-lower-secret":      {"sk-3"}, // a non-canonical key, set directly
		"Content-Type":        {"application/json"},
		"X-Request-Id":        {"req-1"},
	}

	out := core.RedactHeader(in, []string{"X-TEST-KEY", "X-Lower-Secret"})

	want := http.Header{
		"Authorization":       {"[REDACTED]"},
		"Proxy-Authorization": {"[REDACTED]"},
		"Cookie":              {"[REDACTED]", "[REDACTED]"},
		"Set-Cookie":          {"[REDACTED]"},
		"X-Test-Key":          {"[REDACTED]"},
		"x-lower-secret":      {"[REDACTED]"},
		"Content-Type":        {"application/json"},
		"X-Request-Id":        {"req-1"},
	}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("RedactHeader:\n got %v\nwant %v", out, want)
	}

	// Without the adapter's list, only core's four are redacted.
	out = core.RedactHeader(in, nil)
	if got := out.Get("X-Test-Key"); got != "sk-2" {
		t.Fatalf("X-Test-Key with no adapter list = %q, want it kept", got)
	}
	if got := out.Get("Authorization"); got != "[REDACTED]" {
		t.Fatalf("Authorization with no adapter list = %q, want [REDACTED]", got)
	}
}

func TestRedact_QueryKeepsBytes(t *testing.T) {
	secret := []string{"key", "api key"}
	tests := []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"no secret", "a=1&b=%zz&c", "a=1&b=%zz&c"},
		{"semicolon", "a=1;b=2&key=s", "a=1;b=2&key=[REDACTED]"},
		{"bad escape kept", "x=%zz&key=s&y=%ZZ%", "x=%zz&key=[REDACTED]&y=%ZZ%"},
		{"repeated", "key=s1&a=1&key=s2", "key=[REDACTED]&a=1&key=[REDACTED]"},
		{"empty parameters", "&a=1&&key=s&", "&a=1&&key=[REDACTED]&"},
		{"empty value", "key=&a=", "key=[REDACTED]&a="},
		{"bare name has no value", "key&a=1", "key&a=1"},
		{"first equals splits", "key=a=b&c=d=e", "key=[REDACTED]&c=d=e"},
		{"escaped name", "api%20key=s&api+key=t", "api%20key=[REDACTED]&api+key=[REDACTED]"},
		{"case sensitive", "KEY=s", "KEY=s"},
		{"not a prefix", "keys=s&mykey=t", "keys=s&mykey=t"},
		{"escapes elsewhere kept", "q=a%2Bb+c&key=%41", "q=a%2Bb+c&key=[REDACTED]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := core.RedactQuery(tt.in, secret); got != tt.want {
				t.Fatalf("RedactQuery(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}

	in := "a=1;b=%zz&&key=s&key=t&"
	if got := core.RedactQuery(in, nil); got != in {
		t.Fatalf("RedactQuery with no list = %q, want %q", got, in)
	}
}
