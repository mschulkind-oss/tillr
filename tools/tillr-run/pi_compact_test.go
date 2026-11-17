package main_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPiCompactByIDReportsMeasurements(t *testing.T) {
	for _, mode := range []string{"success", "refused", "wrong-session", "no-usage", "missing-tokens", "exit", "journal"} {
		t.Run(mode, func(t *testing.T) {
			dir := scratch(t, "pi-compact")
			bin := filepath.Join(dir, "bin")
			capture := filepath.Join(dir, "commands")
			writeExecutable(t, filepath.Join(bin, "pi"), `#!/usr/bin/env python3
import json,os,sys
assert sys.argv[1:5] == ['--mode','rpc','--session','11111111-1111-4111-8111-111111111111'],sys.argv
mode=os.environ['COMPACT_MODE']
for line in sys.stdin.buffer:
 c=json.loads(line)
 with open(os.environ['CAPTURE'],'a') as f: f.write(c['type']+'\n')
 if mode=='exit': sys.exit(1)
 data={'sessionId':'11111111-1111-4111-8111-111111111111','sessionFile':os.environ['TRANSCRIPT']}
 if c['type']=='get_state' and mode=='wrong-session': data={'sessionId':'different'}
 if c['type']=='compact':
  data={'tokensBefore':50000,'estimatedTokensAfter':20000,'usage':{'totalTokens':1200,'cost':{'total':0.125}}}
  if mode=='no-usage': del data['usage']
  if mode=='missing-tokens': del data['estimatedTokensAfter']
 print(json.dumps({'type':'response','id':c['id'],'success':mode!='refused','data':data,'error':'refused'}),flush=True)
`)
			adapter, err := filepath.Abs("backends/pi/compact")
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(adapter, "--backend", "pi-designer", "--model", "fixture-model", "--effort", "medium", "--session-id", "11111111-1111-4111-8111-111111111111")
			transcript := filepath.Join(dir, "session.jsonl")
			if err := os.WriteFile(transcript, []byte("session\n"), 0644); err != nil {
				t.Fatal(err)
			}
			journal := filepath.Join(dir, "journal.json")
			if err := os.WriteFile(journal, []byte(`{"session_id":"11111111-1111-4111-8111-111111111111","transcript":"old-log","offset":0}`), 0644); err != nil {
				t.Fatal(err)
			}
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "COMPACT_MODE="+mode, "CAPTURE="+capture, "TRANSCRIPT="+transcript)
			if mode == "journal" {
				cmd.Env = append(cmd.Env, "TILLR_COMPACTION_JOURNAL="+journal)
			}
			out, err := cmd.CombinedOutput()
			if mode != "success" && mode != "no-usage" && mode != "journal" {
				if err == nil {
					t.Fatalf("accepted %s: %s", mode, out)
				}
				return
			}
			if err != nil {
				t.Fatalf("%v %s", err, out)
			}
			var receipt struct {
				Before int      `json:"tokens_before"`
				After  int      `json:"tokens_after"`
				Cost   *float64 `json:"cost_usd"`
			}
			if err = json.Unmarshal(out, &receipt); err != nil {
				t.Fatal(err)
			}
			if receipt.Before != 50000 || receipt.After != 20000 || (mode == "success" && (receipt.Cost == nil || *receipt.Cost != 0.125)) || (mode == "no-usage" && receipt.Cost != nil) {
				t.Fatalf("receipt %s", out)
			}
			raw, _ := os.ReadFile(capture)
			if mode == "journal" {
				raw, e := os.ReadFile(journal)
				if e != nil {
					t.Fatal(e)
				}
				var j struct {
					Transcript string `json:"transcript"`
					Offset     int64  `json:"offset"`
				}
				if e = json.Unmarshal(raw, &j); e != nil {
					t.Fatal(e)
				}
				if j.Transcript != transcript || j.Offset != 8 {
					t.Fatalf("journal %s", raw)
				}
			}
			if strings.TrimSpace(string(raw)) != "get_state\ncompact" {
				t.Fatalf("commands %s", raw)
			}
		})
	}
}
