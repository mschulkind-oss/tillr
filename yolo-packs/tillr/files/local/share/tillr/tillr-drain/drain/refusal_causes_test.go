package drain

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func TestComputeRefusalCauses(t *testing.T) {
	tests := []struct {
		name    string
		payload refusalPayload
		want    []RefusalCause
	}{
		{
			name: "gate multi-check sorted and deduplicated",
			payload: refusalPayload{
				Step: RefusalGate,
				Checks: []EndingCheck{
					{
						CheckID: "docs",
						Failing: []FailingCheck{{ID: "documents"}, {ID: "citations"}},
					},
					{
						CheckID: "go-build",
					},
					{
						CheckID: "dup",
						Failing: []FailingCheck{{ID: "documents"}},
					},
				},
			},
			want: []RefusalCause{
				{Cause: "gate:citations"},
				{Cause: "gate:documents"},
				{Cause: "gate:go-build"},
			},
		},
		{
			name: "gate no checks emits gate:no-verdict",
			payload: refusalPayload{
				Step: RefusalGate,
			},
			want: []RefusalCause{{Cause: "gate:no-verdict"}},
		},
		{
			name: "gate wrapper entry without failing cases is omitted",
			payload: refusalPayload{
				Step: RefusalGate,
				Checks: []EndingCheck{
					{CheckID: "gate:python3"},
					{CheckID: "elapsed-time"},
					{
						CheckID: "go-affected",
						Failing: []FailingCheck{{
							ID:    "go-affected",
							Cases: []FailingCase{{Name: "TestCheckDeclarationsCoverSelectionInputs", Assertion: "input x is undeclared"}},
						}},
					},
				},
			},
			want: []RefusalCause{
				{Cause: "gate:elapsed-time"},
				{Cause: "gate:go-affected:TestCheckDeclarationsCoverSelectionInputs:input x is undeclared"},
			},
		},
		{
			name: "gate failing cases key on check plus case plus first assertion line",
			payload: refusalPayload{
				Step: RefusalGate,
				Checks: []EndingCheck{
					{
						CheckID: "gate:python3",
						Failing: []FailingCheck{
							{
								ID: "go-affected",
								Cases: []FailingCase{
									{Name: "TestEveryStoreProgramFlushesTheSQLiteTraceOnExit", Assertion: "trace not flushed"},
									{Name: "TestClassificationScanGoSites", Assertion: "  site a unclassified \nsecond line"},
									{Name: "TestClassificationScanGoSites", Assertion: "site a unclassified"},
								},
							},
						},
					},
				},
			},
			want: []RefusalCause{
				{Cause: "gate:go-affected:TestClassificationScanGoSites:site a unclassified"},
				{Cause: "gate:go-affected:TestEveryStoreProgramFlushesTheSQLiteTraceOnExit:trace not flushed"},
			},
		},
		{
			name: "gate one test failing two different assertions keys two causes",
			payload: refusalPayload{
				Step: RefusalGate,
				Checks: []EndingCheck{{
					CheckID: "go-affected",
					Failing: []FailingCheck{{
						ID: "go-affected",
						Cases: []FailingCase{
							{Name: "TestArchitecture", Assertion: `new domain file "drain/x.go" added to drain/`},
							{Name: "TestArchitecture", Assertion: "function f is 14 lines (max 10 for bridge wrappers)"},
						},
					}},
				}},
			},
			want: []RefusalCause{
				{Cause: "gate:go-affected:TestArchitecture:function f is 14 lines (max 10 for bridge wrappers)"},
				{Cause: `gate:go-affected:TestArchitecture:new domain file "drain/x.go" added to drain/`},
			},
		},
		{
			name: "gate named case without assertion emits no cause and no fallback",
			payload: refusalPayload{
				Step: RefusalGate,
				Checks: []EndingCheck{{
					CheckID: "go-affected",
					Failing: []FailingCheck{{
						ID:    "go-affected",
						Cases: []FailingCase{{Name: "TestArchitecture"}, {Name: "TestOther", Assertion: "  "}},
					}},
				}},
			},
			want: []RefusalCause{},
		},
		{
			name: "gate failing check without named cases falls back to check id",
			payload: refusalPayload{
				Step: RefusalGate,
				Checks: []EndingCheck{
					{
						CheckID: "gate:python3",
						Failing: []FailingCheck{
							{ID: "go-affected", Cases: []FailingCase{{Name: ""}}},
						},
					},
				},
			},
			want: []RefusalCause{{Cause: "gate:go-affected"}},
		},
		{
			name: "gate wrapper entry with triage failing checks keeps inner checks",
			payload: refusalPayload{
				Step: RefusalGate,
				Checks: []EndingCheck{
					{
						CheckID: "gate:python3",
						Failing: []FailingCheck{{ID: "tillr-backends"}},
					},
				},
			},
			want: []RefusalCause{{Cause: "gate:tillr-backends"}},
		},
		{
			name: "gate wrapper entry alone without failing cases emits gate:no-verdict",
			payload: refusalPayload{
				Step: RefusalGate,
				Checks: []EndingCheck{
					{CheckID: "gate:python3"},
				},
			},
			want: []RefusalCause{{Cause: "gate:no-verdict"}},
		},
		{
			name: "staging with staging-failed reason",
			payload: refusalPayload{
				Step:   RefusalStaging,
				Reason: ReasonStagingFailed,
			},
			want: []RefusalCause{{Cause: "staging:staging-failed"}},
		},
		{
			name: "staging with item-unstagleable reason",
			payload: refusalPayload{
				Step:   RefusalStaging,
				Reason: ReasonItemUnstagleable,
			},
			want: []RefusalCause{{Cause: "staging:item-unstagleable"}},
		},
		{
			name: "staging with empty reason",
			payload: refusalPayload{
				Step: RefusalStaging,
			},
			want: []RefusalCause{{Cause: "staging"}},
		},
		{
			name:    "step-level fence",
			payload: refusalPayload{Step: RefusalFence},
			want:    []RefusalCause{{Cause: "fence"}},
		},
		{
			name:    "step-level land",
			payload: refusalPayload{Step: RefusalLand},
			want:    []RefusalCause{{Cause: "land"}},
		},
		{
			name:    "step-level no-op",
			payload: refusalPayload{Step: RefusalNoOp},
			want:    []RefusalCause{{Cause: "no-op"}},
		},
		{
			name:    "step-level node-output-missing",
			payload: refusalPayload{Step: RefusalNodeOutput},
			want:    []RefusalCause{{Cause: "node-output-missing"}},
		},
		{
			name:    "step-level verify-no-verdict",
			payload: refusalPayload{Step: RefusalVerifyNoVerdict},
			want:    []RefusalCause{{Cause: "verify-no-verdict"}},
		},
		{
			name:    "verify-not-done without feedback counts nothing",
			payload: refusalPayload{Step: RefusalVerifyNotDone},
			want:    []RefusalCause{},
		},
		{
			name: "verify-not-done findings without file paths count nothing",
			payload: refusalPayload{Step: RefusalVerifyNotDone, VerifyFeedback: &verifyFeedback{Findings: []verifyFindingRecord{
				{Criterion: "c1", Reason: "missing test"},
				{Criterion: "c2", Reason: "gap", Findings: item.VerdictFindings{{Kind: "gap", Case: "TestX", Paths: []string{"case:TestX"}}}},
			}}},
			want: []RefusalCause{},
		},
		{
			name: "verify-not-done keys on normalized finding paths",
			payload: refusalPayload{Step: RefusalVerifyNotDone, VerifyFeedback: &verifyFeedback{Findings: []verifyFindingRecord{
				{Criterion: "c1", Findings: item.VerdictFindings{{Kind: "gap", Paths: []string{"file:./tools/a.go:12", "file:docs/b.md"}}}},
				{Criterion: "c2", Findings: item.VerdictFindings{{Kind: "regression", Paths: []string{"file:tools/a.go:40"}}}},
			}}},
			want: []RefusalCause{{Cause: "verify-not-done:docs/b.md"}, {Cause: "verify-not-done:tools/a.go"}},
		},
		{
			name:    "step-level plan-wrong",
			payload: refusalPayload{Step: RefusalPlanWrong},
			want:    []RefusalCause{{Cause: "plan-wrong"}},
		},
		{
			name:    "step-level delivery-order",
			payload: refusalPayload{Step: RefusalDeliveryOrder},
			want:    []RefusalCause{{Cause: "delivery-order"}},
		},
		{
			name:    "step-level words-missing",
			payload: refusalPayload{Step: RefusalWordsMissing},
			want:    []RefusalCause{{Cause: "words-missing"}},
		},
		{
			name:    "step-level words-altered",
			payload: refusalPayload{Step: RefusalWordsAltered},
			want:    []RefusalCause{{Cause: "words-altered"}},
		},
		{
			name:    "step-level idea-no-outcome",
			payload: refusalPayload{Step: RefusalIdeaNoOutcome},
			want:    []RefusalCause{{Cause: "idea-no-outcome"}},
		},
		{
			name:    "eval-failed without feedback counts nothing",
			payload: refusalPayload{Step: ReasonEvalFailed},
			want:    []RefusalCause{},
		},
		{
			name: "eval-failed keys on finding paths like verify-not-done",
			payload: refusalPayload{Step: ReasonEvalFailed, VerifyFeedback: &verifyFeedback{Findings: []verifyFindingRecord{
				{Criterion: "c1", Findings: item.VerdictFindings{{Kind: "gap", Paths: []string{"file:tools/a.go:3"}}}},
			}}},
			want: []RefusalCause{{Cause: "verify-not-done:tools/a.go"}},
		},
		{
			name:    "excluded trunk-already-red",
			payload: refusalPayload{Step: RefusalTrunkAlreadyRed},
			want:    []RefusalCause{},
		},
		{
			name:    "excluded tooling-defect",
			payload: refusalPayload{Step: RefusalToolingDefect},
			want:    []RefusalCause{},
		},
		{
			name:    "excluded conflict",
			payload: refusalPayload{Step: RefusalConflict},
			want:    []RefusalCause{},
		},
		{
			name:    "excluded conflict restart",
			payload: refusalPayload{Step: RefusalConflict, Restart: true},
			want:    []RefusalCause{},
		},
		{
			name:    "excluded reverted",
			payload: refusalPayload{Step: RefusalReverted},
			want:    []RefusalCause{},
		},
		{
			name:    "excluded plan-output-unparseable",
			payload: refusalPayload{Step: RefusalPlanOutput},
			want:    []RefusalCause{},
		},
		{
			name:    "excluded judge-unreadable",
			payload: refusalPayload{Step: ReasonJudgeUnreadable},
			want:    []RefusalCause{},
		},
		{
			name:    "excluded judge-blocked",
			payload: refusalPayload{Step: ReasonJudgeBlocked},
			want:    []RefusalCause{},
		},
		{
			name:    "excluded judge-defective",
			payload: refusalPayload{Step: ReasonJudgeDefective},
			want:    []RefusalCause{},
		},
		{
			name:    "excluded judge-defective-evidence-mismatch",
			payload: refusalPayload{Step: ReasonJudgeDefectiveMismatch},
			want:    []RefusalCause{},
		},
		{
			name:    "excluded judge-defective-close-refused",
			payload: refusalPayload{Step: ReasonJudgeCloseRefused},
			want:    []RefusalCause{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := computeRefusalCauses(tc.payload)
			if got == nil {
				t.Fatalf("computeRefusalCauses(%q) returned nil slice, want non-nil slice", tc.payload.Step)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("computeRefusalCauses(%q) = %#v, want %#v", tc.payload.Step, got, tc.want)
			}

			p := tc.payload
			p.RefusalCauses = got
			raw, err := json.Marshal(p)
			if err != nil {
				t.Fatalf("json.Marshal: %v", err)
			}
			if strings.Contains(string(raw), `"refusal_causes":null`) {
				t.Fatalf("marshaled payload has null refusal_causes: %s", raw)
			}
			if !strings.Contains(string(raw), `"refusal_causes":[`) {
				t.Fatalf("marshaled payload missing refusal_causes array: %s", raw)
			}
		})
	}
}

func TestHoldJudgePreservesWriterRunIDAndRefusalCauses(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	storePath := filepath.Join(dir, "state.db")
	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	s.Close()

	l := &Loop{
		cfg: LoopConfig{
			Root:  dir,
			Store: storePath,
			Now:   func() time.Time { return time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC) },
		},
	}

	for _, tc := range []struct {
		judgeID  string
		reason   string
		feedback *verifyFeedback
		want     string
	}{
		{judgeID: "run-judge-1", reason: ReasonEvalFailed, want: `"refusal_causes":[]`},
		{judgeID: "run-judge-2", reason: ReasonJudgeBlocked, want: `"refusal_causes":[]`},
		{judgeID: "run-judge-3", reason: ReasonEvalFailed, feedback: &verifyFeedback{Findings: []verifyFindingRecord{
			{Criterion: "c1", Findings: item.VerdictFindings{{Kind: "gap", Paths: []string{"file:tools/a.go:7"}}}},
		}}, want: `"refusal_causes":[{"cause":"verify-not-done:tools/a.go"}]`},
	} {
		result := &LoopResult{}
		c := Candidate{
			RunID:   "run-writer-1",
			ItemID:  "item-a",
			LaneID:  "lane-1",
			TickID:  "tick-1",
			BaseSHA: "aaaa",
			HeadSHA: "bbbb",
		}
		c.verifyFeedback = tc.feedback
		if err := l.holdJudge(ctx, c, tc.judgeID, tc.reason, "detail", result); err != nil {
			t.Fatalf("holdJudge(%s): %v", tc.reason, err)
		}

		db, err := store.OpenRead(ctx, storePath)
		if err != nil {
			t.Fatalf("store.OpenRead: %v", err)
		}
		var raw string
		err = db.ReadTx(ctx, func(r *store.Reader) error {
			return r.QueryRow(`SELECT payload FROM events WHERE kind = ? AND object_id = ?`, RefusalKind, tc.judgeID).Scan(&raw)
		})
		db.Close()
		if err != nil {
			t.Fatalf("query payload for %s: %v", tc.judgeID, err)
		}
		if !strings.Contains(raw, `"writer_run_id":"run-writer-1"`) {
			t.Fatalf("holdJudge(%s) dropped writer_run_id: %s", tc.reason, raw)
		}
		if !strings.Contains(raw, tc.want) {
			t.Fatalf("holdJudge(%s) payload = %s, want %s", tc.reason, raw, tc.want)
		}
	}
}
