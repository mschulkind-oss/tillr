package heavyrun

import (
	"context"
	"time"

	"github.com/mschulkind-oss/tillr-drain/heavy"
)

type heavyFailingTestsKey struct{}

type heavyFailingTestsState struct {
	storePath string
	trunk     string
	tipSHA    string
	now       func() time.Time
}

// WithHeavyFailingTests binds the store-backed failing-test memory for a trunk proof attempt.
func WithHeavyFailingTests(ctx context.Context, storePath, trunk, tipSHA string, now func() time.Time) context.Context {
	if storePath == "" {
		return ctx
	}
	return context.WithValue(ctx, heavyFailingTestsKey{}, &heavyFailingTestsState{
		storePath: storePath,
		trunk:     trunk,
		tipSHA:    tipSHA,
		now:       now,
	})
}

func LoadHeavyFailingTests(ctx context.Context, checkID string) ([]string, error) {
	st, _ := ctx.Value(heavyFailingTestsKey{}).(*heavyFailingTestsState)
	if st == nil || st.storePath == "" || checkID == "" {
		return nil, nil
	}
	return heavy.LoadFailingTests(ctx, st.storePath, st.trunk, checkID)
}

func SaveHeavyFailingTests(ctx context.Context, checkID string, tests []string) error {
	st, _ := ctx.Value(heavyFailingTestsKey{}).(*heavyFailingTestsState)
	if st == nil || st.storePath == "" || checkID == "" {
		return nil
	}
	now := time.Now().UTC()
	if st.now != nil {
		now = st.now().UTC()
	}
	return heavy.SaveFailingTests(ctx, st.storePath, st.trunk, checkID, st.tipSHA, tests, now)
}

func ClearHeavyFailingTests(ctx context.Context, checkID string) error {
	st, _ := ctx.Value(heavyFailingTestsKey{}).(*heavyFailingTestsState)
	if st == nil || st.storePath == "" || checkID == "" {
		return nil
	}
	return heavy.ClearFailingTests(ctx, st.storePath, st.trunk, checkID)
}
