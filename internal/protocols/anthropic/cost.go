package anthropic

import (
	"bytes"
	_ "embed"
	"encoding/json"

	"github.com/brutally-honest/llm-gateway/internal/cost"
)

// pricesCSV is Anthropic's published API price table (spec 020 AC1).
//
//go:embed prices.csv
var pricesCSV []byte

// CostBook is this protocol's pricing: the embedded table and the reading of a usage
// event's detail. A table that does not parse is a build defect, caught by its test.
func (Adapter) CostBook() cost.Book {
	t, err := cost.ParseTable(bytes.NewReader(pricesCSV))
	if err != nil {
		panic(err)
	}
	return cost.Book{Source: "Anthropic", Table: t, Facts: costFacts}
}

// costFacts reads the cache-write split and the price variant from the usage object's
// keys kept in the detail: cache_creation's ephemeral_5m/1h counts, speed and
// inference_geo (research D6, D8). A key missing or unreadable gives no split or no
// variant.
func costFacts(detail json.RawMessage) cost.Facts {
	var d struct {
		CacheCreation *struct {
			FiveMin *int64 `json:"ephemeral_5m_input_tokens"`
			OneHour *int64 `json:"ephemeral_1h_input_tokens"`
		} `json:"cache_creation"`
		Speed        string `json:"speed"`
		InferenceGeo string `json:"inference_geo"`
	}
	if json.Unmarshal(detail, &d) != nil {
		return cost.Facts{}
	}
	var f cost.Facts
	if cc := d.CacheCreation; cc != nil && cc.FiveMin != nil && cc.OneHour != nil {
		f.Split = &cost.Split{FiveMin: *cc.FiveMin, OneHour: *cc.OneHour}
	}
	switch {
	case d.Speed != "" && d.Speed != "standard":
		f.Variant = d.Speed
	case d.InferenceGeo != "" && d.InferenceGeo != "global":
		f.Variant = d.InferenceGeo
	}
	return f
}
