package core_test

import "github.com/brutally-honest/llm-gateway/internal/core"

// parsingAdapter is the test adapter with both optional extensions: its own secret
// headers and query parameters, and a parser. Core must accept it without knowing it.
type parsingAdapter struct {
	testAdapter
}

func (parsingAdapter) SecretHeaders() []string     { return []string{"X-Test-Key"} }
func (parsingAdapter) SecretQueryParams() []string { return []string{"key"} }
func (parsingAdapter) Parser() core.Parser         { return testParser{} }

// testParser turns nothing into canonical events: it only proves the seam compiles
// for a protocol core has never heard of.
type testParser struct{}

func (testParser) Parse(core.ParseInput) core.ParseResult {
	return core.ParseResult{Status: core.ParseSkipped}
}

func (testParser) HashExcludedFields() []string { return []string{excludedKey} }

// Compile-time proof that a test adapter can implement both optional interfaces, and
// that it is still an Adapter.
var (
	_ core.Adapter        = parsingAdapter{}
	_ core.SecretDeclarer = parsingAdapter{}
	_ core.ParsingAdapter = parsingAdapter{}
	_ core.Parser         = testParser{}
)
