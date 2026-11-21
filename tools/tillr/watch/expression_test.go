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

func TestWatchExpressionSyntax(t *testing.T) {
	for _, text := range []string{
		`seat not in pair or item.state in charter.finished`,
		`unproved_commits == 0`,
		`git.ref("refs/heads/main") != git.ref("refs/tillr/green")`,
		`item("item-1").state == "done"`,
		`age(event.ts) > 1`,
		`event.seat not in ["plan"]`,
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
