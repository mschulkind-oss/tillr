package watch

import (
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Expression is a parsed boolean predicate. Unsupported forms are parsed for
// validity, but cannot be evaluated by the event-only evaluator.
type Expression struct {
	root        operand
	unsupported bool
}

type operand func(lookup func(string) any) any

func (e *Expression) Supported() bool { return !e.unsupported }

// Eval looks up event names (without the "event." prefix). A nil lookup result
// means absent or null. Use int64 for integer columns and json.Decoder.UseNumber
// for payloads so integers are not rounded. Unsupported expressions never match.
func (e *Expression) Eval(lookup func(string) any) bool {
	return e.Supported() && truth(e.root(lookup))
}

func truth(v any) bool      { b, _ := v.(bool); return b }
func literal(v any) operand { return func(func(string) any) any { return v } }

// ParseExpression checks the full predicate syntax and compiles RE2 patterns.
func ParseExpression(text string) (*Expression, error) {
	p := parser{text: text}
	p.next()
	root := p.or()
	if p.tok.kind != "end" {
		p.fail("unexpected token %q", p.tok.text)
	}
	if p.err != nil {
		return nil, p.err
	}
	return &Expression{root: root, unsupported: p.unsupported}, nil
}

type token struct {
	kind, text string
	at         int
}

type parser struct {
	text        string
	off         int
	tok         token
	err         error
	unsupported bool
}

func (p *parser) fail(format string, args ...any) {
	if p.err == nil {
		p.err = fmt.Errorf("expression at byte %d: %s", p.tok.at+1, fmt.Sprintf(format, args...))
	}
}

func identStart(r rune) bool { return r == '_' || unicode.IsLetter(r) }
func identRest(r rune) bool  { return identStart(r) || unicode.IsDigit(r) || r == '-' }
func digit(b byte) bool      { return b >= '0' && b <= '9' }

func (p *parser) next() {
	input := p.text[p.off:]
	s := strings.TrimLeftFunc(input, unicode.IsSpace)
	start := p.off + len(input) - len(s)
	p.tok = token{kind: "end", at: start}
	if s == "" || p.err != nil {
		return
	}
	kind, n := "", 1
	first, width := utf8.DecodeRuneInString(s)
	switch {
	case identStart(first):
		kind, n = "name", width
		for n < len(s) {
			r, w := utf8.DecodeRuneInString(s[n:])
			if !identRest(r) {
				break
			}
			n += w
		}
	case digit(s[0]) || (s[0] == '-' || s[0] == '+') && len(s) > 1 && digit(s[1]):
		kind = "integer"
		for n < len(s) && digit(s[n]) {
			n++
		}
	case s[0] == '"':
		kind = "string"
		for n < len(s) && s[n] != '"' {
			if s[n] == '\\' && n+1 < len(s) {
				n++
			}
			n++
		}
		if n == len(s) {
			p.fail("unterminated string")
			return
		}
		n++
	case strings.ContainsRune("=!<>", first) && len(s) > 1 && s[1] == '=':
		kind, n = s[:2], 2
	case strings.ContainsRune("<>()[].,", first):
		kind = s[:1]
	default:
		p.fail("unexpected character %q", first)
		return
	}
	p.tok = token{kind: kind, text: s[:n], at: start}
	p.off = start + n
}

func (p *parser) take(text string) bool {
	if p.tok.text != text {
		return false
	}
	p.next()
	return true
}

func (p *parser) expect(text string) {
	if !p.take(text) {
		p.fail("expected %q", text)
	}
}

func (p *parser) or() operand {
	left := p.and()
	for p.take("or") {
		a, b := left, p.and()
		left = func(lookup func(string) any) any { return truth(a(lookup)) || truth(b(lookup)) }
	}
	return left
}

func (p *parser) and() operand {
	left := p.not()
	for p.take("and") {
		a, b := left, p.not()
		left = func(lookup func(string) any) any { return truth(a(lookup)) && truth(b(lookup)) }
	}
	return left
}

func (p *parser) not() operand {
	if p.take("not") {
		arg := p.not()
		return func(lookup func(string) any) any { return !truth(arg(lookup)) }
	}
	return p.comparison()
}

func (p *parser) term() operand {
	if p.take("(") {
		arg := p.or()
		p.expect(")")
		return arg
	}
	arg, _ := p.value()
	return arg
}

func (p *parser) comparison() operand {
	left := p.term()
	op := p.tok.text
	switch op {
	case "in", "not":
		p.next()
		if op == "not" {
			p.expect("in")
			p.unsupported = true
		}
		right, kind := p.value()
		if kind != "list" && kind != "bare" && kind != "call" {
			p.fail("expected list, bare name or call after in")
		}
		return func(lookup func(string) any) any {
			list, _ := right(lookup).([]any)
			found := containValue(list, left(lookup))
			return found != (op == "not")
		}
	case "matches":
		p.next()
		if p.tok.kind != "string" {
			p.fail("matches requires a quoted regular expression")
			return nil
		}
		pattern := p.stringValue()
		re, err := regexp.Compile(pattern)
		if err != nil {
			p.fail("invalid RE2 regular expression: %v", err)
			return nil
		}
		return func(lookup func(string) any) any {
			s, ok := left(lookup).(string)
			return ok && re.MatchString(s)
		}
	case "==", "!=", "<", "<=", ">", ">=", "contains":
		p.next()
		right := p.term()
		return func(lookup func(string) any) any { return compare(op, left(lookup), right(lookup)) }
	default:
		return left
	}
}

func (p *parser) stringValue() string {
	s, err := strconv.Unquote(p.tok.text)
	if err != nil {
		p.fail("invalid double-quoted string")
	}
	p.next()
	return s
}

// value parses an operand, not a comparison or connective. The syntactic
// category is also needed for the right side of membership.
func (p *parser) value() (operand, string) {
	t := p.tok
	switch t.kind {
	case "integer":
		n, err := strconv.ParseInt(t.text, 10, 64)
		if err != nil {
			p.fail("invalid integer: %v", err)
		}
		p.next()
		return literal(n), "integer"
	case "string":
		return literal(p.stringValue()), "string"
	case "[":
		p.next()
		args := p.values("]")
		return func(lookup func(string) any) any {
			list := make([]any, len(args))
			for i, arg := range args {
				list[i] = arg(lookup)
			}
			return list
		}, "list"
	case "name":
		p.next()
		switch t.text {
		case "true", "false":
			return literal(t.text == "true"), "bool"
		case "not", "and", "or", "in", "matches", "contains":
			p.fail("expected operand, found operator %q", t.text)
			return nil, ""
		}
		name := t.text + p.dotted()
		if p.take("(") {
			args := p.values(")")
			suffix := p.dotted()
			if name == "empty" && suffix == "" {
				if len(args) != 1 {
					p.fail("empty requires one operand")
					return nil, ""
				}
				return func(lookup func(string) any) any { return empty(args[0](lookup)) }, "call"
			}
			p.unsupported = true
			return literal(nil), "call"
		}
		if field, ok := strings.CutPrefix(name, "event."); ok {
			return func(lookup func(string) any) any { return lookup(field) }, "event"
		}
		p.unsupported = true
		return literal(nil), "bare"
	default:
		p.fail("expected an operand")
		return nil, ""
	}
}

func (p *parser) dotted() string {
	var name strings.Builder
	for p.take(".") {
		if p.tok.kind != "name" {
			p.fail("expected identifier after dot")
			break
		}
		name.WriteByte('.')
		name.WriteString(p.tok.text)
		p.next()
	}
	return name.String()
}

func (p *parser) values(end string) []operand {
	if p.take(end) {
		return nil
	}
	var args []operand
	for p.err == nil {
		arg, _ := p.value()
		args = append(args, arg)
		if p.take(end) {
			break
		}
		p.expect(",")
	}
	return args
}

func integer(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	default:
		return 0, false
	}
}

func empty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	default:
		return false
	}
}

func containValue(list []any, v any) bool {
	for _, member := range list {
		if compare("==", v, member) {
			return true
		}
	}
	return false
}

func contains(a, b any) bool {
	switch x := a.(type) {
	case string:
		y, ok := b.(string)
		return ok && strings.Contains(x, y)
	case []any:
		return containValue(x, b)
	default:
		return false
	}
}

func compare(op string, a, b any) bool {
	if a == nil || b == nil {
		return false
	}
	if op == "contains" {
		return contains(a, b)
	}
	if op == "==" || op == "!=" {
		same, compatible := equal(a, b)
		return compatible && same == (op == "==")
	}
	x, xi := integer(a)
	y, yi := integer(b)
	switch op {
	case "<":
		return xi && yi && x < y
	case "<=":
		return xi && yi && x <= y
	case ">":
		return xi && yi && x > y
	case ">=":
		return xi && yi && x >= y
	default:
		return false
	}
}

// numberText exposes the two numeric representations the evaluator uses.
func numberText(v any) string {
	switch n := v.(type) {
	case int64:
		return strconv.FormatInt(n, 10)
	case json.Number:
		return string(n)
	default:
		return ""
	}
}

// decimalParts normalizes a valid JSON number into signed digits and a base-10
// exponent. Keeping the exponent separate avoids rounding and never expands
// large exponents into strings of zeroes.
func decimalParts(text string) (string, *big.Int) {
	exp := new(big.Int)
	if i := strings.IndexAny(text, "eE"); i >= 0 {
		exp.SetString(text[i+1:], 10)
		text = text[:i]
	}
	text, negative := strings.CutPrefix(text, "-")
	var fraction int
	if whole, part, ok := strings.Cut(text, "."); ok {
		fraction = len(part)
		text = whole + part
	}
	text = strings.TrimLeft(text, "0")
	if text == "" {
		return "0", exp.SetInt64(0)
	}
	digits := strings.TrimRight(text, "0")
	exp.Add(exp, big.NewInt(int64(len(text)-len(digits)-fraction)))
	if negative {
		digits = "-" + digits
	}
	return digits, exp
}

// equal keeps a type mismatch distinct from inequality and compares event
// JSON numbers and integer literals by numeric value, even within lists.
func equal(a, b any) (same, compatible bool) {
	if x, xi := integer(a); xi {
		if y, yi := integer(b); yi {
			return x == y, true
		}
	}
	switch x := a.(type) {
	case int64, json.Number:
		y := numberText(b)
		if y == "" {
			return false, false
		}
		aDigits, aExp := decimalParts(numberText(a))
		bDigits, bExp := decimalParts(y)
		return aDigits == bDigits && aExp.Cmp(bExp) == 0, true
	case nil:
		return b == nil, b == nil
	case string:
		y, ok := b.(string)
		return x == y, ok
	case bool:
		y, ok := b.(bool)
		return x == y, ok
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false, ok
		}
		for i, v := range x {
			if same, compatible := equal(v, y[i]); !same || !compatible {
				return false, true
			}
		}
		return true, true
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false, ok
		}
		for key, v := range x {
			w, present := y[key]
			if same, compatible := equal(v, w); !present || !same || !compatible {
				return false, true
			}
		}
		return true, true
	default:
		return false, false
	}
}
