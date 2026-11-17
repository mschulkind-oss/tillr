// Who already holds the address `serve` was asked to listen on.
//
// "The last way to serve the wrong board is to serve an old process's" (the
// observatory-dev recipe's comment). A second `just observatory` on a held port used to die
// on `bind: address already in use`, and the owner kept looking at the browser tab the OLD
// process was answering. This file turns that bind failure into one of three plain answers:
// the holder IS this build serving this store (open it; nothing to do), the holder is an
// observatory of another build or store (stop it, with the command), or the holder is not an
// observatory at all.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// holderVerdict is what is on the port: "same" (this build, this store), "other" (an
// observatory that is not this one), or "foreign" (something that does not answer as one).
type holderVerdict struct {
	Kind    string
	Message string
}

// probeAddress is where to dial for a listen address: a wildcard bind is dialled on
// loopback, which is where any local listener on that port answers.
func probeAddress(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

func addrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}

// describeHolder asks the holder for its snapshot envelope, which every observatory answers
// with its build and store, refusal or not.
func describeHolder(ctx context.Context, addr string, self buildStamp, storePath string) holderVerdict {
	dial := probeAddress(addr)
	_, port, _ := net.SplitHostPort(dial)
	stop := fmt.Sprintf("stop it (`fuser -k %s/tcp`, or `ss -ltnp 'sport = :%s'` to name the process) and run this again, or pick another port", port, port)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+dial+"/api/v1/snapshot", nil)
	if err != nil {
		return holderVerdict{"foreign", fmt.Sprintf("%s is already in use: %s", addr, stop)}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return holderVerdict{"foreign", fmt.Sprintf("%s is already in use by something that does not answer HTTP: %s", addr, stop)}
	}
	defer resp.Body.Close()
	var body struct {
		Snapshot *struct {
			StorePath string      `json:"store_path"`
			Build     *buildStamp `json:"build"`
		} `json:"snapshot"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.Snapshot == nil {
		return holderVerdict{"foreign", fmt.Sprintf("%s is already in use by something that is not an observatory: %s", addr, stop)}
	}
	held := body.Snapshot.Build
	if held == nil {
		return holderVerdict{"other", fmt.Sprintf("%s is held by an observatory older than the build stamp, so it is not this build: %s", addr, stop)}
	}
	sameStore := sameFile(body.Snapshot.StorePath, storePath)
	sameBuild := held.Digest != "" && held.Digest == self.Digest
	if sameBuild && sameStore {
		return holderVerdict{"same", fmt.Sprintf("%s is already this build (%s) serving %s: open http://%s", addr, shortRev(self), storePath, dial)}
	}
	what := fmt.Sprintf("an observatory built from %s (%s) serving %s", shortRev(*held), held.Executable, body.Snapshot.StorePath)
	if sameBuild {
		return holderVerdict{"other", fmt.Sprintf("%s is held by this build serving another store (%s): %s", addr, body.Snapshot.StorePath, stop)}
	}
	return holderVerdict{"other", fmt.Sprintf("%s is held by %s, not this build (%s): %s", addr, what, shortRev(self), stop)}
}

func shortRev(b buildStamp) string {
	rev := b.Revision
	if rev == "" {
		rev = "no revision"
	} else if len(rev) > 12 {
		rev = rev[:12]
	}
	if b.Modified {
		rev += "+dirty"
	}
	return rev
}

// sameFile compares two store paths as the processes named them. A relative path from
// another process's working directory cannot be resolved here, so only absolute paths that
// clean to the same name count as one store.
func sameFile(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	absB, err := filepath.Abs(b)
	if err != nil {
		return false
	}
	if !filepath.IsAbs(a) {
		return strings.TrimPrefix(filepath.Clean(a), "./") == strings.TrimPrefix(filepath.Clean(b), "./")
	}
	return filepath.Clean(a) == filepath.Clean(absB)
}
