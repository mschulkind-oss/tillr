package watch

import (
	"encoding/json"
	"testing"
)

func TestWatchExpressionValues(t *testing.T) {
	values := map[string]any{
		"kind": "gate.verdict", "detail.step-name": "plan", "pass": false,
		"seq": int64(3), "count": json.Number("3"), "decimal": json.Number("3.5"),
		"decimal-alt": json.Number("350e-2"), "whole-decimal": json.Number("3.0"),
		"huge": json.Number("1e999999999999999999999"), "huge-alt": json.Number("10e999999999999999999998"),
		"precise": json.Number("3.500000000000000000001"), "negative-zero": json.Number("-0.00e7"),
		"tags": []any{"one", json.Number("3"), nil}, "empty-list": []any{},
		"empty-object": map[string]any{}, "null": nil, "blank": "",
	}
	for _, tt := range []struct {
		text string
		want bool
	}{
		{`true or false and false`, true},
		{`(true or false) and false`, false},
		{`not event.pass == false`, false},
		{`not event.pass == true and false`, false},
		{`event.seq > -2 and event.seq <= 3 and event.count >= 3`, true},
		{`event.detail.step-name in ["work", "plan", 3]`, true},
		{`event.seq in [event.count]`, true},
		{`event.seq in []`, false},
		{`event.seq not in [1, 2]`, true},
		{`event.seq not in [1, 2, 3]`, false},
		{`[event.count] == [3]`, true},
		{`[event.count] != [3]`, false},
		{`event.tags contains 3`, true},
		{`event.tags contains "3"`, false},
		{`event.kind contains "verdict"`, true},
		{`event.kind contains 3`, false},
		{`event.kind matches "^gate[.]verdict$"`, true},
		{`event.seq matches "3"`, false},
		{`event.seq != "3"`, false},
		{`event.pass != 0`, false},
		{`"a" < "b" or event.decimal > 1`, false},
		{`event.decimal == event.decimal-alt`, true},
		{`event.decimal != event.precise`, true},
		{`event.huge == event.huge-alt`, true},
		{`event.negative-zero == 0`, true},
		{`event.whole-decimal == event.seq and event.seq == event.whole-decimal`, true},
		{`event.decimal >= event.decimal-alt or event.whole-decimal >= 3`, false},
		{`event.absent != 3 or event.null != event.absent`, false},
		{`event.absent in [event.null]`, false},
		{`event.tags contains event.absent`, false},
		{`empty(event.absent) and empty(event.null) and empty(event.blank)`, true},
		{`empty(event.empty-list) and empty(event.empty-object)`, true},
		{`empty(event.pass) or empty(event.seq) or empty(event.tags)`, false},
	} {
		t.Run(tt.text, func(t *testing.T) {
			exp, err := ParseExpression(tt.text)
			if err != nil {
				t.Fatal(err)
			}
			if !exp.Supported() || exp.Eval(func(name string) any { return values[name] }) != tt.want {
				t.Fatalf("want evaluated value %t", tt.want)
			}
		})
	}
}

func TestWatchExpressionProjectionValues(t *testing.T) {
	values := expressionValues{
		event: func(field string) any {
			return map[string]any{"ts": int64(1050), "future": int64(4130), "badtime": "1050", "item.state": "fake"}[field]
		},
		projection: func(ref projectionOperand) any {
			return map[projectionOperand]any{
				{name: "item.state"}: "closed", {name: "item.waiting_on"}: nil,
				{name: "charter.finished"}:                []any{"closed", "failed"},
				{name: "git.ref", ref: "refs/heads/main"}: "abc",
			}[ref]
		},
		nowMillis: 3000,
	}
	missing, err := ParseExpression(`item.waiting_on not in ["owner"]`)
	if err != nil || missing.eval(values) {
		t.Fatalf("missing value must not satisfy not in: %v", err)
	}
	for _, text := range []string{
		`age(event.ts) == 1 and age(event.future) == -1 and empty(age(event.badtime))`,
		`item.state in charter.finished and empty(item.waiting_on) and not item.waiting_on != "none"`,
		`git.ref("refs/heads/main") == "abc" and item.state != event.item.state`,
	} {
		e, err := ParseExpression(text)
		if err != nil || e.Supported() || e.Eval(values.event) || !truth(e.root(values)) {
			t.Fatalf("want compiled, true with projections, and gated in event-only eval: %q (%v)", text, err)
		}
	}
}

func TestWatchExpressionFixerProvedAndUsesEvent(t *testing.T) {
	exp, err := ParseExpression(`item.id matches "^fixer-main-" and item.state == "in-queue" and item.fixer_proved("refs/tillr/green")`)
	if err != nil {
		t.Fatal(err)
	}
	if exp.Supported() || exp.usesEvent {
		t.Fatalf("unexpected flags: supported=%v usesEvent=%v", exp.Supported(), exp.usesEvent)
	}
	wantOp := projectionOperand{name: "item.fixer_proved", ref: "refs/tillr/green"}
	if len(exp.projections) != 3 || exp.projections[2] != wantOp {
		t.Fatalf("projections = %+v, want [2]=%+v", exp.projections, wantOp)
	}
	values := expressionValues{
		projection: func(op projectionOperand) any {
			switch op {
			case projectionOperand{name: "item.id"}:
				return "fixer-main-18f649ca0940"
			case projectionOperand{name: "item.state"}:
				return "in-queue"
			case wantOp:
				return true
			default:
				return nil
			}
		},
	}
	if !exp.eval(values) {
		t.Fatal("expected expression to evaluate true")
	}

	evExp, err := ParseExpression(`item.state == "in-queue" and event.kind == "gate.verdict"`)
	if err != nil {
		t.Fatal(err)
	}
	if !evExp.usesEvent {
		t.Fatal("expected usesEvent=true when event.* is referenced")
	}

	for _, bad := range []string{
		`item.fixer_proved()`,
		`item.fixer_proved(event.ref)`,
		`item.fixer_proved("refs/tillr/green", "extra")`,
		`item.fixer_proved("refs/tillr/green").extra`,
		`item.fixer_proved("main")`,
		`item.fixer_proved("refs/tillr/green~1")`,
	} {
		if _, err := ParseExpression(bad); err == nil {
			t.Errorf("%s: want syntax error", bad)
		}
	}
}

func TestWatchExpressionSyntax(t *testing.T) {
	for _, text := range []string{
		`seat not in pair or item.state in charter.finished`,
		`unproved_commits == 0`,
		`git.ref("refs/heads/main") != git.ref("refs/tillr/green")`,
		`item("item-1").state == "done"`,
		`age(event.ts) > 1`,
		`empty(foo(event.ts, 1, ["x"])).state`,
		`event.seat in allowed()`,
	} {
		if exp, err := ParseExpression(text); err != nil || exp.Supported() {
			t.Errorf("%s: want accepted but unevaluated, got %v, %v", text, exp, err)
		}
	}
	for _, text := range []string{
		``, `event.`, `event..kind`, `event.kind = "x"`, `event.kind == 'x'`,
		`event.kind ==`, `true true`, `true && false`, `1 < 2 < 3`, `(true`,
		`event.kind in "a"`, `event.kind in event.list`, `event.kind in ["a",]`,
		`event.kind matches event.kind`, `event.kind matches "["`, `event.kind matches "(?=a)"`,
		`empty()`, `empty(true, false)`, `event.seq == 1.5`, `event.seq == 1e2`,
		`item(,).state`, `item().`,
	} {
		if _, err := ParseExpression(text); err == nil {
			t.Errorf("%s: want syntax error", text)
		}
	}
}
