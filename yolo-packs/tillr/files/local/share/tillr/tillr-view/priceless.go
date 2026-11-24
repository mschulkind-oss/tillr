package main

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"strings"
)

const priceNumber = `[-+]?(?:[0-9][0-9,.]*|\.[0-9]+)(?:[eE][-+]?[0-9]+)?`

var currencyAmount = regexp.MustCompile(`[$€£¥]\s*` + priceNumber + `(?:\s*(?:USD|usd))?`)
var namedDollarAmount = regexp.MustCompile(`(?i)(?:\bUSD\s*` + priceNumber + `|` + priceNumber + `\s*(?:USD|(?:US )?dollars)\b|\b(?:[a-z]+[ _])*cost[ _]usd\s*:\s*` + priceNumber + `)`)
var embeddedPrice = regexp.MustCompile(`(?i)"[^"\n]*cost[^"\n]*"\s*:\s*(?:[0-9.eE+\-]+|null)`)

// precleaned wraps a Data value whose fields have already passed through withoutPrices.
type precleaned struct {
	raw any
}

func marshalClean(body any) (json.RawMessage, error) {
	clean, err := withoutPrices(body)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(clean)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}

// The HTTP face intentionally excludes pricing, including in trace attributes and
// artifact/transcript strings. Stored records and the trace CLI remain unchanged.
func withoutPrices(body any) (any, error) {
	if p, ok := body.(precleaned); ok {
		return p.raw, nil
	}
	if resp, ok := body.(apiResponse); ok {
		if p, ok := resp.Data.(precleaned); ok {
			snap, err := withoutPrices(resp.Snapshot)
			if err != nil {
				return nil, err
			}
			return map[string]any{"snapshot": snap, "data": p.raw}, nil
		}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	var value any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	return stripPrices(value), nil
}
func stripPrices(value any) any {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if strings.Contains(strings.ToLower(key), "cost") {
				delete(v, key)
			} else {
				v[key] = stripPrices(child)
			}
		}
	case []any:
		filtered := v[:0]
		for _, child := range v {
			// OTLP represents attribute names as values, not JSON property names.
			if attribute, ok := child.(map[string]any); ok {
				if key, ok := attribute["key"].(string); ok && strings.Contains(strings.ToLower(key), "cost") {
					continue
				}
			}
			filtered = append(filtered, stripPrices(child))
		}
		return filtered
	case string:
		// Serialized attributes and raw artifact JSON are strings inside an API answer.
		var child any
		dec := json.NewDecoder(strings.NewReader(v))
		dec.UseNumber()
		if (strings.HasPrefix(strings.TrimSpace(v), "{") || strings.HasPrefix(strings.TrimSpace(v), "[")) && dec.Decode(&child) == nil && dec.Decode(new(any)) == io.EOF {
			raw, err := json.Marshal(stripPrices(child))
			if err == nil {
				return string(raw)
			}
		}
		if lines := strings.Split(v, "\n"); len(lines) > 1 {
			for i, line := range lines {
				lines[i] = stripPrices(line).(string)
			}
			return strings.Join(lines, "\n")
		}
		v = embeddedPrice.ReplaceAllString(v, `"price_omitted":null`)
		v = currencyAmount.ReplaceAllString(v, "[price omitted]")
		v = namedDollarAmount.ReplaceAllString(v, "[price omitted]")
		return strings.Map(func(r rune) rune {
			if strings.ContainsRune("$€£¥", r) {
				return -1
			}
			return r
		}, v)
	}
	return value
}
