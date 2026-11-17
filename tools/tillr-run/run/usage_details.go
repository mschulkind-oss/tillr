package run

// UsageDetails preserves the adapter's token kinds and streaming clocks in the
// terminal event. Older stores need no migration: absence stays absence.
type UsageDetails struct {
	Models        []ModelUsage `json:"models"`
	Turns         []TurnUsage  `json:"turns"`
	FirstOutputMS *float64     `json:"first_output_ms,omitempty"`
	OutputTPS     *float64     `json:"output_tokens_per_second,omitempty"`
}

type ModelUsage struct {
	Tokens     *int64  `json:"tokens,omitempty"`
	Model      *string `json:"model"`
	Input      *int64  `json:"input,omitempty"`
	Output     *int64  `json:"output,omitempty"`
	CacheRead  *int64  `json:"cache_read,omitempty"`
	CacheWrite *int64  `json:"cache_write,omitempty"`
}

type TurnUsage struct {
	ModelUsage
	ID              string   `json:"id"`
	StartedMS       *float64 `json:"started_at_ms,omitempty"`
	FinishedMS      *float64 `json:"finished_at_ms,omitempty"`
	FirstOutputAtMS *float64 `json:"first_output_at_ms,omitempty"`
	FirstOutputMS   *float64 `json:"first_output_ms,omitempty"`
	OutputTPS       *float64 `json:"output_tokens_per_second,omitempty"`
}
