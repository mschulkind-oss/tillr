package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func judgeFace(args []string) int {
	fs := newFlagSet("judge")
	path := fs.String("store", "", "the store to read or write")
	id := fs.String("item", "", "the item whose complete criteria are judged")
	input := fs.String("input", "", "the JSON verdict file (evaluated_head, evaluated_tree, outcome, verdicts)")
	head := fs.String("head", "", "the candidate commit SHA in the current Git repository")
	actor := fs.String("actor", "", "caller-owned judge identity: node:run_ids:route or run=<id>;session=<id>")
	judged := fs.String("judged-actor", "", "caller-owned writer identity: run_ids:routes or run=<id>;session=<id>")
	workflow := fs.String("workflow", "", "the persisted workflow reference")
	node := fs.String("evaluator-node", "", "the persisted evaluator node")
	digest := fs.String("graph-sha256", "", "SHA-256 of the exact persisted expanded graph")
	landed := fs.String("landed", "", "the landed commit SHA to add to each done verdict's evidence")
	dry := fs.Bool("dry-run", false, "validate read-only and print outcome; exits: 0 pass, 10 defective, 11 blocked, 12 not-done; refusals 1, usage 2, incompatible store 3, external failure 4, internal error 5")
	now := fs.String("now", "", "the injected clock, RFC 3339 (default: current UTC time)")
	if err := parseFlags(fs, args); err != nil {
		return fail(err)
	}
	if *path == "" || *id == "" || *input == "" || *head == "" || *actor == "" || *judged == "" || *workflow == "" || *node == "" || *digest == "" {
		return fail(item.Usagef("judge needs --store, --item, --input, --head, --actor, --judged-actor, --workflow, --evaluator-node and --graph-sha256"))
	}
	at := time.Now().UTC()
	if *now != "" {
		var err error
		at, err = time.Parse(time.RFC3339, *now)
		if err != nil {
			return fail(item.Usagef("--now: %v", err))
		}
	}
	raw, err := os.ReadFile(*input)
	if err != nil {
		return fail(item.Usagef("reading judge input: %v", err))
	}
	resolved, err := judgeCommit(*head)
	if err != nil {
		return fail(err)
	}
	if resolved != *head {
		return fail(item.Usagef("--head must be a full commit SHA, not a symbolic or abbreviated ref"))
	}
	tree, err := judgeGitObject(*head + "^{tree}")
	if err != nil {
		return fail(err)
	}
	if *landed != "" {
		sha, err := judgeCommit(*landed)
		if err != nil {
			return fail(err)
		}
		if sha != *landed {
			return fail(item.Usagef("--landed must be a full commit SHA"))
		}
	}
	options := item.JudgeOptions{ItemID: *id, Head: resolved, Tree: tree, Actor: *actor, JudgedActor: *judged, EvaluatorBinding: item.EvaluatorBinding{Workflow: *workflow, EvaluatorNode: *node, GraphSHA256: *digest}, Landed: *landed, At: at}
	ctx := context.Background()
	var result item.JudgeResult
	if *dry {
		s, err := store.OpenRead(ctx, *path)
		if err != nil {
			return fail(openFailure(err))
		}
		defer s.Close()
		err = s.ReadTx(ctx, func(r *store.Reader) error {
			var err error
			result, err = item.CheckJudge(ctx, r, raw, options)
			return err
		})
		if err != nil {
			return printJudgeError(err, at)
		}
	} else {
		s, err := store.Open(ctx, *path, true)
		if err != nil {
			return fail(openFailure(err))
		}
		defer s.Close()
		err = s.WriteTx(ctx, func(tx *store.Tx) error {
			var err error
			result, err = item.RecordJudge(ctx, tx, raw, options)
			return err
		})
		if err != nil {
			return printJudgeError(err, at)
		}
	}
	fmt.Println(result.Outcome)
	switch result.Outcome {
	case item.VerdictDefective:
		return 10
	case item.VerdictBlocked:
		return 11
	case item.VerdictNotDone:
		return 12
	default:
		return record.ExitOK
	}
}

func printJudgeError(err error, at time.Time) int {
	env, code := rejectionOrFailure(err, at)
	if env.Kind != "" {
		raw, err := encodeEnvelope(env)
		if err != nil {
			return fail(item.Internalf("encoding judge rejection: %v", err))
		}
		fmt.Println(string(raw))
	}
	return code
}

func judgeCommit(sha string) (string, error) { return judgeGitObject(sha + "^{commit}") }
func judgeGitObject(ref string) (string, error) {
	out, err := exec.Command("git", "rev-parse", "--verify", "--end-of-options", ref).CombinedOutput()
	if err != nil {
		return "", item.Usagef("resolving candidate Git object %q: %v: %s", ref, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
