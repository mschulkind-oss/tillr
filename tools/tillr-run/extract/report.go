package extract

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
)

const (
	ExitDone         = 0
	ExitFailed       = 1
	ExitUsage        = 2
	ExitBlocked      = 3
	ExitIncomplete   = 4
	ExitTrunkBroken  = 5
	ExitHarnessEnded = 6

	Unknown = "unknown"
)

var (
	footerPattern    = regexp.MustCompile(`^RESULT: (done|blocked|failed|trunk-broken)\s*$`)
	footerResultExit = map[string]int{
		"done":         ExitDone,
		"failed":       ExitFailed,
		"blocked":      ExitBlocked,
		"trunk-broken": ExitTrunkBroken,
	}
	headerKeys = []string{
		"backend",
		"model",
		"route",
		"effort",
		"session",
		"exit code",
		"started",
		"finished",
		"stream lines",
		"events",
		"unparseable lines",
		"tokens",
		"cost usd",
		"rate limit kind",
		"fraction",
		"status",
		"reset",
		"complete",
		"result",
	}
)

type EndedInfo struct {
	Cause  string
	Detail string
}

type WindowInfo struct {
	RateLimitType string
	Utilization   *float64
	Status        string
	ResetsAt      any
}

type DialectResult struct {
	Text         string
	Session      string
	Model        string
	Started      string
	Finished     string
	Lines        *int
	Events       int
	Unparseable  int
	Tokens       *int64
	Cost         *float64
	Window       *WindowInfo
	Ended        *EndedInfo
	UsageDetails *run.UsageDetails
}

type DialectFunc func(streamText string, liveProbe bool) DialectResult

// SplitLines matches Python's str.splitlines() for LF, CRLF, and CR line endings.
func SplitLines(s string) []string {
	if s == "" {
		return nil
	}
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\r' {
			lines = append(lines, s[start:i])
			if i+1 < len(s) && s[i+1] == '\n' {
				i++
			}
			start = i + 1
		} else if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func FooterResult(text string) string {
	lines := SplitLines(text)
	for i := len(lines) - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		m := footerPattern.FindStringSubmatch(trimmed)
		if m != nil {
			return m[1]
		}
		return ""
	}
	return ""
}

func TokensText(tokens *int64) string {
	if tokens == nil {
		return Unknown
	}
	return strconv.FormatInt(*tokens, 10)
}

func CostText(cost *float64) string {
	if cost == nil {
		return Unknown
	}
	return fmt.Sprintf("%.6f", *cost)
}

type RenderInput struct {
	Backend     string
	Model       string
	Route       string
	Effort      string
	Session     string
	ExitCode    string
	Started     string
	Finished    string
	StreamLines int
	Events      int
	Unparseable int
	Tokens      *int64
	Cost        *float64
	Text        string
	Window      *WindowInfo
	Ended       string
}

func orUnknown(s string) string {
	if s == "" {
		return Unknown
	}
	return s
}

func formatReset(v any) string {
	if v == nil {
		return Unknown
	}
	switch val := v.(type) {
	case string:
		if val == "" {
			return Unknown
		}
		return val
	case float64:
		if val == 0 {
			return Unknown
		}
		if val == math.Trunc(val) && val >= -1e15 && val <= 1e15 {
			return strconv.FormatInt(int64(val), 10)
		}
		return strconv.FormatFloat(val, 'g', -1, 64)
	case int64:
		if val == 0 {
			return Unknown
		}
		return strconv.FormatInt(val, 10)
	case int:
		if val == 0 {
			return Unknown
		}
		return strconv.Itoa(val)
	case bool:
		if !val {
			return Unknown
		}
		return "true"
	default:
		return fmt.Sprint(v)
	}
}

func Render(in RenderInput) (string, string) {
	result := FooterResult(in.Text)
	rateLimitKind := Unknown
	fraction := Unknown
	status := Unknown
	reset := Unknown
	if in.Window != nil {
		if in.Window.RateLimitType != "" {
			rateLimitKind = in.Window.RateLimitType
		}
		if in.Window.Utilization != nil {
			fraction = strconv.FormatFloat(*in.Window.Utilization, 'g', -1, 64)
		}
		if in.Window.Status != "" {
			status = in.Window.Status
		}
		reset = formatReset(in.Window.ResetsAt)
	}

	complete := "no"
	if result != "" {
		complete = "yes"
	}
	resultVal := result
	if resultVal == "" {
		if in.Ended != "" {
			resultVal = "ended by the harness: " + in.Ended
		} else {
			resultVal = "missing"
		}
	}

	header := map[string]string{
		"backend":           orUnknown(in.Backend),
		"model":             orUnknown(in.Model),
		"route":             orUnknown(in.Route),
		"effort":            orUnknown(in.Effort),
		"session":           orUnknown(in.Session),
		"exit code":         orUnknown(in.ExitCode),
		"started":           orUnknown(in.Started),
		"finished":          orUnknown(in.Finished),
		"stream lines":      strconv.Itoa(in.StreamLines),
		"events":            strconv.Itoa(in.Events),
		"unparseable lines": strconv.Itoa(in.Unparseable),
		"tokens":            TokensText(in.Tokens),
		"cost usd":          CostText(in.Cost),
		"rate limit kind":   rateLimitKind,
		"fraction":          fraction,
		"status":            status,
		"reset":             reset,
		"complete":          complete,
		"result":            resultVal,
	}

	var b strings.Builder
	b.WriteString("# run report\n\n")
	for _, key := range headerKeys {
		b.WriteString(key)
		b.WriteString(": ")
		b.WriteString(header[key])
		b.WriteByte('\n')
	}
	b.WriteString("\n---\n")
	b.WriteString(in.Text)
	if in.Text != "" && !strings.HasSuffix(in.Text, "\n") {
		b.WriteByte('\n')
	}
	return b.String(), result
}

func Records(streamText string) ([]map[string]any, int) {
	var parsed []map[string]any
	unparseable := 0
	for _, line := range SplitLines(streamText) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var value map[string]any
		if err := json.Unmarshal([]byte(line), &value); err != nil || value == nil {
			unparseable++
			continue
		}
		parsed = append(parsed, value)
	}
	return parsed, unparseable
}

func MicrosToRFC3339(micros float64) string {
	sec := int64(micros / 1_000_000)
	nsec := int64((micros - float64(sec)*1_000_000) * 1000)
	if nsec < 0 {
		sec--
		nsec += 1_000_000_000
	}
	t := time.Unix(sec, nsec).UTC()
	return t.Format("2006-01-02T15:04:05Z")
}

func RunCLI(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	const prog = "extract-report"
	fs := flag.NewFlagSet(prog, flag.ContinueOnError)
	fs.SetOutput(stderr)
	dialectName := fs.String("dialect", "", "the backend dialect (pi, claude, agy, muse, native)")
	backend := fs.String("backend", "", "the adapter name, from staging")
	model := fs.String("model", "", "the model id staging supplied")
	route := fs.String("route", "", "the route staging asked for")
	effort := fs.String("effort", "", "the effort staging supplied")
	sessionID := fs.String("session-id", "", "overrides the session id the stream carries")
	exitCode := fs.String("exit-code", Unknown, "the harness's exit code, as staging saw it")
	outPath := fs.String("out", "", "report path; default stdout")
	landingOffer := fs.String("landing-offer", "", "commit offer artifact from the finished run")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitDone
		}
		return ExitUsage
	}
	if fs.NArg() > 1 {
		fmt.Fprintf(stderr, "%s: unexpected argument %q\n", prog, fs.Arg(1))
		return ExitUsage
	}
	streamPath := "-"
	if fs.NArg() == 1 {
		streamPath = fs.Arg(0)
	}

	var dialectFn DialectFunc
	switch *dialectName {
	case "pi":
		dialectFn = PiDialect
	case "claude":
		dialectFn = func(s string, _ bool) DialectResult { return ClaudeDialect(s) }
	case "agy":
		dialectFn = func(s string, _ bool) DialectResult { return AgyDialect(s, stderr) }
	case "muse":
		dialectFn = func(s string, _ bool) DialectResult { return MuseDialect(s) }
	case "native":
		dialectFn = NativeDialect
	default:
		fmt.Fprintf(stderr, "%s: unknown dialect %q (want pi, claude, agy, muse, native)\n", prog, *dialectName)
		return ExitUsage
	}

	var streamBytes []byte
	var err error
	if streamPath == "-" {
		streamBytes, err = io.ReadAll(stdin)
	} else {
		streamBytes, err = os.ReadFile(streamPath)
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s: reading the stream: %v\n", prog, err)
		return ExitUsage
	}
	streamText := strings.ToValidUTF8(string(streamBytes), "\ufffd")

	liveProbe := false
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--exit-code" && args[i+1] == Unknown {
			liveProbe = true
		}
	}

	found := dialectFn(streamText, liveProbe)
	text := found.Text

	if *landingOffer != "" {
		offerBytes, err := os.ReadFile(*landingOffer)
		if err != nil {
			fmt.Fprintf(stderr, "%s: reading the landing offer: %v\n", prog, err)
		} else {
			var offer map[string]any
			if err := json.Unmarshal(offerBytes, &offer); err != nil || offer == nil {
				fmt.Fprintf(stderr, "%s: reading the landing offer: %v\n", prog, err)
			} else {
				tool, hasTool := offer["tool"]
				toolOK := !hasTool || tool == nil || tool == "land"
				keysOK := true
				for _, k := range []string{"call_id", "base", "commit"} {
					v, ok := offer[k].(string)
					if !ok || strings.TrimSpace(v) == "" {
						keysOK = false
						break
					}
				}
				if toolOK && keysOK {
					text = "RESULT: done\n"
				}
			}
		}
	}

	chosenModel := *model
	if chosenModel == "" {
		chosenModel = found.Model
	}
	chosenSession := *sessionID
	if chosenSession == "" {
		chosenSession = found.Session
	}
	streamLines := len(SplitLines(streamText))
	if found.Lines != nil {
		streamLines = *found.Lines
	}
	endedCause := ""
	if found.Ended != nil {
		endedCause = found.Ended.Cause
	}

	reportText, result := Render(RenderInput{
		Backend:     *backend,
		Model:       chosenModel,
		Route:       *route,
		Effort:      *effort,
		Session:     chosenSession,
		ExitCode:    *exitCode,
		Started:     found.Started,
		Finished:    found.Finished,
		StreamLines: streamLines,
		Events:      found.Events,
		Unparseable: found.Unparseable,
		Tokens:      found.Tokens,
		Cost:        found.Cost,
		Text:        text,
		Window:      found.Window,
		Ended:       endedCause,
	})

	if *outPath != "" {
		if err := os.WriteFile(*outPath, []byte(reportText), 0o644); err != nil {
			fmt.Fprintf(stderr, "%s: writing the report: %v\n", prog, err)
			return ExitUsage
		}
		var usageJSON []byte
		if found.UsageDetails == nil {
			usageJSON = []byte("null")
		} else {
			usageJSON, err = json.Marshal(found.UsageDetails)
			if err != nil {
				fmt.Fprintf(stderr, "%s: writing the report: %v\n", prog, err)
				return ExitUsage
			}
		}
		if err := os.WriteFile(*outPath+".usage.json", usageJSON, 0o644); err != nil {
			fmt.Fprintf(stderr, "%s: writing the report: %v\n", prog, err)
			return ExitUsage
		}
	} else {
		if _, err := io.WriteString(stdout, reportText); err != nil {
			fmt.Fprintf(stderr, "%s: writing the report: %v\n", prog, err)
			return ExitUsage
		}
	}

	if result == "" && found.Ended != nil {
		fmt.Fprintf(stderr, "%s: ENDED BY THE HARNESS: %s\n", prog, found.Ended.Detail)
		return ExitHarnessEnded
	}
	if result == "" {
		where := *outPath
		if where == "" {
			where = "stdout"
		}
		fmt.Fprintf(stderr, "%s: INCOMPLETE: the stream carries no 'RESULT: done|blocked|failed' footer, so the report at %s is not a verdict. An empty or truncated stream is the usual cause; the remedy is a smaller brief, not more effort.\n", prog, where)
		return ExitIncomplete
	}
	return footerResultExit[result]
}
