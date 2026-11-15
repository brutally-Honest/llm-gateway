package cost

// CallJSON is a call's cost as the API sends it. Amounts are exact decimal USD strings
// (research D7); the browser rounds them for display only.
type CallJSON struct {
	Status    string     `json:"status"`
	Reason    string     `json:"reason,omitempty"`
	Source    string     `json:"source,omitempty"`
	USD       string     `json:"usd,omitempty"` // set only when priced
	Estimated bool       `json:"estimated,omitempty"`
	AtLeast   bool       `json:"at_least,omitempty"`
	Lines     []LineJSON `json:"lines,omitempty"`
}

// LineJSON is one token kind of a call's breakdown.
type LineJSON struct {
	Kind       string `json:"kind"`
	Tokens     int64  `json:"tokens"`
	USDPerMTok string `json:"usd_per_mtok"`
	USD        string `json:"usd"`
}

// JSON is the call's API shape.
func (c Call) JSON() CallJSON {
	j := CallJSON{Status: c.Status, Reason: c.Reason, Source: c.Source, Estimated: c.Estimated, AtLeast: c.AtLeast}
	if c.Status == StatusPriced {
		j.USD = USD(c.Amount)
		for _, l := range c.Lines {
			j.Lines = append(j.Lines, LineJSON{Kind: l.Kind, Tokens: l.Tokens, USDPerMTok: RateUSD(l.Rate), USD: USD(l.Amount)})
		}
	}
	return j
}

// TotalJSON is a total's API shape: the sum of the priced calls and the counts that say
// why it may be partial or estimated.
type TotalJSON struct {
	USD           string   `json:"usd"`
	Source        string   `json:"source,omitempty"`
	Priced        int      `json:"priced"`
	NoPrice       int      `json:"no_price"`
	NoPriceModels []string `json:"no_price_models"`
	NoUsage       int      `json:"no_usage"`
	Errored       int      `json:"errored"`
	Estimated     int      `json:"estimated"`
	AtLeast       int      `json:"at_least"`
	Partial       bool     `json:"partial"`
}

// JSON is the total's API shape.
func (t Total) JSON() TotalJSON {
	models := t.NoPriceModels
	if models == nil {
		models = []string{}
	}
	return TotalJSON{USD: USD(t.Amount), Source: t.Source, Priced: t.Priced, NoPrice: t.NoPrice, NoPriceModels: models,
		NoUsage: t.NoUsage, Errored: t.Errored, Estimated: t.Estimated, AtLeast: t.AtLeast, Partial: t.Partial()}
}
