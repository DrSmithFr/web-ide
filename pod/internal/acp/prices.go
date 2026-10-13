package acp

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"sync"
)

// Price of a model in dollars per million tokens.
type Price struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

// prices are the API prices of the models (2026-10). Cache reads are the published ones, or
// a tenth of the input; cache writes 1.25 × the input (5 minute cache). <data>/prices.json
// adds or replaces models.
var prices = map[string]Price{
	"claude-fable-5-1":  {10, 50, 0.25, 12.5},
	"claude-fable-5":    {10, 50, 1, 12.5},
	"claude-opus-5-5":   {4, 20, 0.2, 5},
	"claude-opus-5":     {5, 25, 0.5, 6.25},
	"claude-opus-4-8":   {5, 25, 0.5, 6.25},
	"claude-opus-4-7":   {5, 25, 0.5, 6.25},
	"claude-opus-4-6":   {5, 25, 0.5, 6.25},
	"claude-sonnet-5-5": {2, 10, 0.2, 2.5},
	"claude-sonnet-5":   {2, 10, 0.2, 2.5},
	"claude-sonnet-4-6": {3, 15, 0.3, 3.75},
	"claude-haiku-5-5":  {0.1, 0.5, 0.01, 0.125},
	"claude-haiku-4-5":  {1, 5, 0.1, 1.25},
}

var (
	priceMu   sync.Mutex
	priceFile string
	extra     map[string]Price
	extraMod  int64
)

// SetPriceFile names the file of the prices added by the user (<data>/prices.json: a map of
// model to Price), read again when it changes.
func SetPriceFile(path string) {
	priceMu.Lock()
	priceFile, extra, extraMod = path, nil, 0
	priceMu.Unlock()
}

var dated = regexp.MustCompile(`-\d{8}$`)

// normModel drops the provider ("anthropic/") and the date of a model id.
func normModel(m string) string {
	m = strings.ToLower(m)
	if i := strings.LastIndexByte(m, '/'); i >= 0 {
		m = m[i+1:]
	}
	return dated.ReplaceAllString(m, "")
}

func priceOf(model string) (Price, bool) {
	priceMu.Lock()
	defer priceMu.Unlock()
	if priceFile != "" {
		if st, err := os.Stat(priceFile); err == nil && st.ModTime().UnixNano() != extraMod {
			extra, extraMod = nil, st.ModTime().UnixNano()
			if data, err := os.ReadFile(priceFile); err == nil {
				_ = json.Unmarshal(data, &extra)
			}
		}
	}
	for _, m := range []string{model, normModel(model)} {
		if p, ok := extra[m]; ok {
			return p, true
		}
		if p, ok := prices[m]; ok {
			return p, true
		}
	}
	return Price{}, false
}

// cost of tokens; false when a model has no price.
func cost(tokens []Tokens) (float64, bool) {
	var sum float64
	for _, t := range tokens {
		if t.total() == 0 {
			continue
		}
		p, ok := priceOf(t.Model)
		if !ok {
			return 0, false
		}
		sum += (float64(t.Input)*p.Input + float64(t.Output)*p.Output + float64(t.CacheRead)*p.CacheRead + float64(t.CacheWrite)*p.CacheWrite) / 1e6
	}
	return sum, true
}
