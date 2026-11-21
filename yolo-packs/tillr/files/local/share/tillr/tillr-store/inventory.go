// Architecture and invariants: docs/reference/tillr-queue-persistence.md
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/record"
)

// File rows are disjoint: table and ref rows have no byte count, so summing
// byte-bearing rows measures files once, not the database twice.
type inventoryRow struct {
	Part        string `json:"part"`
	Name        string `json:"name"`
	Grain       string `json:"grain"`
	Count       int64  `json:"count"`
	Bytes       *int64 `json:"bytes,omitempty"`
	Description string `json:"description"`
}
type inventoryTotal struct {
	Directory string `json:"directory"`
	Bytes     int64  `json:"bytes"`
	RowBytes  int64  `json:"row_bytes"`
}
type inventoryRun struct {
	RunID            string `json:"run_id"`
	Bytes            int64  `json:"bytes"`
	LargestFile      string `json:"largest_file"`
	LargestFileBytes int64  `json:"largest_file_bytes"`
}
type inventoryReport struct {
	Rows        []inventoryRow   `json:"rows"`
	LaneTotals  []inventoryRow   `json:"lane_totals"`
	Totals      []inventoryTotal `json:"totals"`
	LargestRuns []inventoryRun   `json:"largest_runs"`
}

func inventoryFace(args []string) (record.Envelope, int) {
	flags := newFlagSet("inventory")
	root := flags.String("root", "", "repository whose .tillr directory and refs/tillr refs to inventory (read-only)")
	asJSON := flags.Bool("json", false, "emit one store.inventory record instead of tab-separated rows")
	if code := parseFlags(flags, args); code != record.ExitOK {
		return record.Envelope{}, code
	}
	if *root == "" {
		return record.Envelope{}, usagef("inventory needs --root <repo>")
	}
	absolute, err := filepath.Abs(*root)
	if err != nil {
		return record.Envelope{}, usagef("%v", err)
	}
	result, err := inventory(context.Background(), absolute)
	if err != nil {
		return record.Envelope{}, openFailure(err)
	}
	if !*asJSON {
		printInventory(result)
		return record.Envelope{}, record.ExitOK
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return record.Envelope{}, internalf("%v", err)
	}
	return record.Envelope{
		RecordVersion: record.SupportedVersion, Kind: "store.inventory", ID: newID(),
		Producer:  record.Producer{System: record.System, Utility: utility, Version: version},
		CreatedAt: time.Now().UTC(), Payload: raw,
	}, record.ExitOK
}

func inventory(ctx context.Context, root string) (inventoryReport, error) {
	result := inventoryReport{Rows: []inventoryRow{}, LaneTotals: []inventoryRow{}, Totals: []inventoryTotal{}, LargestRuns: []inventoryRun{}}
	dir := filepath.Join(root, ".tillr")
	// Do not invent a store for a typo, or follow a linked store directory.
	info, err := os.Lstat(dir)
	if err != nil {
		return result, err
	}
	if !info.IsDir() {
		return result, fmt.Errorf("inventory: %s is not a directory", dir)
	}
	tables, err := inventoryTables(ctx, filepath.Join(dir, "state.db"))
	if err != nil {
		return result, err
	}
	result.Rows = append(result.Rows, tables...)

	grouped := map[string]*inventoryRow{}
	laneTotals := map[string]*inventoryRow{}
	runFiles := map[string]map[string]bool{}
	runs := map[string]*inventoryRun{}
	totals := map[string]int64{".tillr": 0, ".tillr/loop": 0, ".tillr/lanes": 0}
	add := func(part, name, grain, description string, size int64) {
		key := part + "\x00" + name
		row := grouped[key]
		if row == nil {
			row = &inventoryRow{Part: part, Name: name, Grain: grain, Bytes: new(int64), Description: description}
			grouped[key] = row
		}
		row.Count++
		*row.Bytes += size
	}
	err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		segments := strings.Split(filepath.ToSlash(relative), "/")
		// Every real lane has both rows, even an absent or empty worktree.
		if entry.IsDir() {
			if len(segments) == 2 && segments[0] == "loop" {
				runs[segments[1]] = &inventoryRun{RunID: segments[1]}
			}
			if len(segments) == 2 && segments[0] == "lanes" {
				for _, category := range []string{"worktree", "other"} {
					name := segments[1] + "/" + category
					description := "Lane source checkout and its build outputs; shows the cost of keeping the worktree."
					if category == "other" {
						description = "Lane briefs, process records and scratch outside the worktree; shows the remaining lane storage."
					}
					laneTotals[name] = &inventoryRow{Part: "lanes", Name: name, Grain: "files", Bytes: new(int64), Description: description}
				}
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		// Lstat sizes include a symlink's own length, never its target. Special
		// entries are measured by metadata only, never opened (notably FIFOs).
		size := info.Size()
		totals[".tillr"] += size
		switch {
		case len(segments) >= 3 && segments[0] == "loop":
			totals[".tillr/loop"] += size
			id := segments[1]
			name := segments[len(segments)-1]
			add("loop", name, "runs", inventoryDescription(loopDescriptions, name), size)
			if runFiles[name] == nil {
				runFiles[name] = map[string]bool{}
			}
			runFiles[name][id] = true
			r := runs[id]
			if r == nil {
				r = &inventoryRun{RunID: id}
				runs[id] = r
			}
			r.Bytes += size
			file := strings.Join(segments[2:], "/")
			if r.LargestFile == "" || size > r.LargestFileBytes || (size == r.LargestFileBytes && file < r.LargestFile) {
				r.LargestFile = file
				r.LargestFileBytes = size
			}
		case len(segments) >= 3 && segments[0] == "lanes":
			totals[".tillr/lanes"] += size
			category := "other"
			if segments[2] == "worktree" {
				category = "worktree"
			}
			name := segments[1] + "/" + category
			summary := laneTotals[name]
			summary.Count++
			*summary.Bytes += size
			file := strings.Join(segments[2:], "/")
			if category == "worktree" {
				file = strings.Join(segments[3:], "/")
			}
			if file != "" {
				name += "/" + file
			}
			description := "undescribed"
			// Source filenames have repository-specific meanings: report.md in
			// a checkout is not necessarily a worker outcome report.
			if category == "other" {
				description = inventoryDescription(loopDescriptions, segments[len(segments)-1])
			}
			add("lanes", name, "files", description, size)
		default:
			// Top-level files and unrecognized directories remain fully visible.
			part := "files"
			if segments[0] == "loop" || segments[0] == "lanes" {
				part = segments[0]
				totals[".tillr/"+part] += size
			}
			name := filepath.ToSlash(relative)
			if part != "files" {
				name = "./" + strings.Join(segments[1:], "/")
			}
			add(part, name, "files", inventoryDescription(fileDescriptions, filepath.ToSlash(relative)), size)
		}
		return nil
	})
	if err != nil {
		return result, fmt.Errorf("inventory: walking files: %w", err)
	}
	for _, row := range grouped {
		if row.Part == "loop" && row.Grain == "runs" {
			row.Count = int64(len(runFiles[row.Name]))
		}
		result.Rows = append(result.Rows, *row)
	}
	for _, row := range laneTotals {
		result.LaneTotals = append(result.LaneTotals, *row)
	}
	sort.Slice(result.LaneTotals, func(i, j int) bool { return result.LaneTotals[i].Name < result.LaneTotals[j].Name })
	refs, err := inventoryRefs(ctx, root)
	if err != nil {
		return result, err
	}
	result.Rows = append(result.Rows, refs...)
	sort.Slice(result.Rows, func(i, j int) bool {
		a, b := result.Rows[i], result.Rows[j]
		if a.Part != b.Part {
			return a.Part < b.Part
		}
		return a.Name < b.Name
	})
	for _, directory := range []string{".tillr", ".tillr/loop", ".tillr/lanes"} {
		var sum int64
		for _, row := range result.Rows {
			if row.Bytes != nil && (directory == ".tillr" || row.Part == strings.TrimPrefix(directory, ".tillr/")) {
				sum += *row.Bytes
			}
		}
		result.Totals = append(result.Totals, inventoryTotal{Directory: directory, Bytes: totals[directory], RowBytes: sum})
	}
	for _, r := range runs {
		result.LargestRuns = append(result.LargestRuns, *r)
	}
	sort.Slice(result.LargestRuns, func(i, j int) bool {
		a, b := result.LargestRuns[i], result.LargestRuns[j]
		if a.Bytes != b.Bytes {
			return a.Bytes > b.Bytes
		}
		return a.RunID < b.RunID
	})
	if len(result.LargestRuns) > 10 {
		result.LargestRuns = result.LargestRuns[:10]
	}
	return result, nil
}

func inventoryTables(ctx context.Context, path string) ([]inventoryRow, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("inventory: %s is not a regular database file", path)
	}
	// mode=ro refuses database writes; readonly_shm=1 also opens SQLite's
	// coordination file read-only, preventing read marks from changing its
	// bytes or mtime. Unlike immutable=1, this still reads committed WAL rows.
	// A transaction gives all table counts one view of committed rows, even while
	// another connection holds a write transaction. No schema-version gate hides
	// a table this binary does not know yet.
	uri := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&readonly_shm=1"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	names, err := tx.QueryContext(ctx, `SELECT name FROM sqlite_schema WHERE type='table' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var tables []string
	for names.Next() {
		var name string
		if err := names.Scan(&name); err != nil {
			names.Close()
			return nil, err
		}
		tables = append(tables, name)
	}
	err = names.Err()
	names.Close()
	if err != nil {
		return nil, err
	}
	rows := []inventoryRow{}
	for _, name := range tables {
		var count int64
		identifier := `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM `+identifier).Scan(&count); err != nil {
			return nil, fmt.Errorf("inventory: counting table %q: %w", name, err)
		}
		rows = append(rows, inventoryRow{Part: "state.db", Name: name, Grain: "rows", Count: count, Description: inventoryDescription(tableDescriptions, name)})
	}
	return rows, nil
}

func inventoryRefs(ctx context.Context, root string) ([]inventoryRow, error) {
	// Git searches parents by default. A directory below another repository
	// must not borrow that repository's refs when --root names no checkout.
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return nil, fmt.Errorf("inventory: repository metadata: %w", err)
	}
	cmd := exec.CommandContext(ctx, "git", "-C", root, "for-each-ref", "--format=%(refname)", "refs/tillr/")
	for _, variable := range os.Environ() {
		name, _, _ := strings.Cut(variable, "=")
		switch name {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_NAMESPACE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_OPTIONAL_LOCKS":
			continue
		}
		cmd.Env = append(cmd.Env, variable)
	}
	cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("inventory: reading refs: %w: %s", err, strings.TrimSpace(string(out)))
	}
	counts := map[string]int64{}
	for _, ref := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if ref == "" {
			continue
		}
		if !strings.HasPrefix(ref, "refs/tillr/") {
			return nil, fmt.Errorf("inventory: unexpected ref %q", ref)
		}
		segment := strings.SplitN(strings.TrimPrefix(ref, "refs/tillr/"), "/", 2)[0]
		counts[segment]++
	}
	rows := []inventoryRow{}
	for name, count := range counts {
		rows = append(rows, inventoryRow{Part: "refs/tillr", Name: name, Grain: "refs", Count: count, Description: inventoryDescription(refDescriptions, name)})
	}
	return rows, nil
}

func printInventoryRows(rows []inventoryRow) {
	fmt.Println("PART\tNAME\tGRAIN\tCOUNT\tBYTES\tWHAT YOU LEARN")
	for _, row := range rows {
		size := "-"
		if row.Bytes != nil {
			size = fmt.Sprint(*row.Bytes)
		}
		fmt.Printf("%q\t%q\t%s\t%d\t%s\t%s\n", row.Part, row.Name, row.Grain, row.Count, size, row.Description)
	}
}

func printInventory(result inventoryReport) {
	printInventoryRows(result.Rows)
	fmt.Println("LANE TOTALS (summaries, not additional file bytes)")
	printInventoryRows(result.LaneTotals)
	fmt.Println("DIRECTORY\tTOTAL BYTES\tROW BYTES")
	for _, total := range result.Totals {
		fmt.Printf("%q\t%d\t%d\n", total.Directory, total.Bytes, total.RowBytes)
	}
	fmt.Println("LARGEST RUNS\tBYTES\tLARGEST FILE\tFILE BYTES")
	for _, r := range result.LargestRuns {
		fmt.Printf("%q\t%d\t%q\t%d\n", r.RunID, r.Bytes, r.LargestFile, r.LargestFileBytes)
	}
}

func inventoryDescription(descriptions map[string]string, name string) string {
	if description := descriptions[name]; description != "" {
		return description
	}
	return "undescribed"
}

var fileDescriptions = map[string]string{
	"state.db":                  "SQLite database file; shows the bytes holding the queue and its durable history.",
	"state.db-wal":              "SQLite write-ahead log; shows bytes allocated for changes not yet fully copied into the database.",
	"state.db-shm":              "SQLite shared-memory coordination file; shows the cost of coordinating live readers and writers.",
	"state.db-checkpoint-owner": "Checkpoint ownership lock file; shows the file used to coordinate checkpoint cycles.",
}
var loopDescriptions = map[string]string{
	"git-trace2.jsonl": "Git process traces; shows which runs retain evidence of Git commands and their cost.",
	"proc-exec.jsonl":  "Process execution traces; shows which runs retain evidence of child commands.",
	"steps.jsonl":      "Loop decision records; shows which runs retain the sequence of loop steps.",
	"stream.jsonl":     "Backend process output; shows which runs retain raw worker output.",
	"transcript.jsonl": "Worker conversation records; shows which runs retain the worker conversation.",
	"report.md":        "Worker outcome reports; shows which runs retain the extracted outcome.",
	"node-report.md":   "Workflow node reports; shows which runs retain the preceding node's outcome.",
	"graph-exit.json":  "Workflow graph exit records; shows which runs retain the workflow's final outcome.",
	"replies.jsonl":    "Document comment replies; shows which runs retain replies from their worker.",
	"stream.err":       "Backend standard error; shows which runs retain process diagnostics.",
	"brief.md":         "Worker task briefs; shows which runs retain the instructions sent to the worker.",
	"fence.txt":        "Allowed change paths; shows which runs retain their change boundary.",
	"diff.patch":       "Candidate diffs; shows which runs retain the proposed source change.",
	"diff.names":       "Changed path lists; shows which runs retain the names used in boundary checks.",
}
var tableDescriptions = map[string]string{
	"meta":              "Store metadata; shows the number of recorded store settings and definitions.",
	"schema_migrations": "Schema migration history; shows which schema steps have been recorded.",
	"sqlite_sequence":   "SQLite sequence counters; shows how many automatically numbered tables have counters.",
	"events":            "Append-only event history; shows the number of durable events available for analysis.",
	"items":             "Queue items; shows the number of work definitions held in the store.",
	"item_criteria":     "Item criteria; shows the number of acceptance requirements attached to work.",
	"item_edges":        "Item dependencies; shows the number of recorded ordering constraints.",
	"item_requests":     "Item requests; shows the number of recorded requests for work.",
	"decisions":         "Item decisions; shows the number of recorded rulings on work.",
	"mail_deliveries":   "Message deliveries; shows the number of recorded deliveries to workers.",
	"lanes":             "Execution lanes; shows the number of registered worker slots.",
	"runs":              "Execution runs; shows the number of worker attempts recorded in the store.",
	"landings":          "Landing outcomes; shows the number of recorded integrations into trunk.",
	"candidates":        "Landing candidates; shows the number of proposed integrations.",
	"landing_attempts":  "Landing attempts; shows the number of recorded attempts to integrate work.",
}
var refDescriptions = map[string]string{
	"runs":      "Run candidate references; shows how many run tips Git retains for inspection.",
	"preserved": "Preserved worker references; shows how many worker tips Git keeps across recovery.",
}
