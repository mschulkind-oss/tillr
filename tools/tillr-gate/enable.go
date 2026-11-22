package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/record"
)

type enabledState struct {
	CheckID     string   `json:"check_id"`
	Episode     string   `json:"episode"`
	Fingerprint string   `json:"fingerprint"`
	PassAt      string   `json:"pass_at"`
	FailAt      string   `json:"fail_at"`
	EnabledAt   string   `json:"enabled_at"`
	Episodes    []string `json:"episodes,omitempty"`
}

func checkStoreKey(checkID string) string {
	sum := sha256.Sum256([]byte(checkID))
	return hex.EncodeToString(sum[:])
}

func episodeStoreKey(checkID, episode string) string {
	sum := sha256.Sum256([]byte(checkID + "|" + episode))
	return hex.EncodeToString(sum[:])
}

func (store resultStore) disabledDir() string {
	return filepath.Join(store.dir, "disabled")
}

func (store resultStore) disabledPath(checkID string) string {
	return filepath.Join(store.disabledDir(), checkStoreKey(checkID)+".json")
}

func (store resultStore) enabledPath(checkID string) string {
	return filepath.Join(store.dir, "enabled", checkStoreKey(checkID)+".json")
}

func (store resultStore) resolvedDir(checkID, episode string) string {
	return filepath.Join(store.dir, "resolved", episodeStoreKey(checkID, episode))
}

func (store resultStore) lockCheck(checkID string) (*os.File, error) {
	dir := filepath.Join(store.dir, "locks")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, checkStoreKey(checkID)), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func publishStoreJSON(path string, value any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".tmp-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(raw); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func (store resultStore) readDisabled(checkID string) (*checkresult.DisabledMarker, error) {
	path := store.disabledPath(checkID)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var marker checkresult.DisabledMarker
	if err := json.Unmarshal(raw, &marker); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if marker.CheckID != "" && marker.CheckID != checkID {
		return nil, fmt.Errorf("invalid disabled marker %s: check_id %q != %q", path, marker.CheckID, checkID)
	}
	marker.CheckID = checkID
	if marker.Fingerprint == "" || marker.PassAt == "" || marker.FailAt == "" {
		return nil, fmt.Errorf("invalid disabled marker %s", path)
	}
	if marker.Episode == "" {
		marker.Episode = checkresult.FormatEpisode(marker.Fingerprint, marker.PassAt, marker.FailAt)
	}
	if fp, passAt, failAt, err := checkresult.ParseEpisode(marker.Episode); err != nil ||
		fp != marker.Fingerprint || passAt != marker.PassAt || failAt != marker.FailAt {
		return nil, fmt.Errorf("invalid disabled marker episode in %s", path)
	}
	return &marker, nil
}

func (store resultStore) readEnabled(checkID string) (*enabledState, error) {
	path := store.enabledPath(checkID)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state enabledState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if len(state.Episodes) == 0 && state.Episode != "" {
		state.Episodes = []string{state.Episode}
	}
	return &state, nil
}

func (store resultStore) copyDisabledLog(checkID, episode, suffix, srcPath string) (string, error) {
	if srcPath == "" {
		return "", nil
	}
	dir := store.disabledDir()
	dst := filepath.Join(dir, episodeStoreKey(checkID, episode)+"."+suffix+".log")
	if filepath.Clean(srcPath) == filepath.Clean(dst) {
		return dst, nil
	}
	src, err := os.Open(srcPath)
	if err != nil {
		if os.IsNotExist(err) {
			return srcPath, nil
		}
		return "", err
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return srcPath, nil
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".log-")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return "", err
	}
	return dst, nil
}

func (store resultStore) writeRule3Marker(checkID string, res checkresult.Result) (checkresult.Result, error) {
	passLog, err := store.copyDisabledLog(checkID, res.Episode, ResultPass, res.PassLogRef)
	if err != nil {
		return res, err
	}
	failLog, err := store.copyDisabledLog(checkID, res.Episode, ResultFail, res.FailLogRef)
	if err != nil {
		return res, err
	}
	marker := checkresult.DisabledMarker{
		CheckID:     checkID,
		Episode:     res.Episode,
		Fingerprint: res.Fingerprint,
		PassAt:      res.PassAt,
		PassLogRef:  passLog,
		FailAt:      res.FailAt,
		FailLogRef:  failLog,
		RepairItem:  res.RepairItem,
	}
	if err := publishStoreJSON(store.disabledPath(checkID), marker); err != nil {
		return res, err
	}
	res.PassLogRef = passLog
	res.FailLogRef = failLog
	return res, nil
}

func moveIntoArchiveIfPresent(src, archiveDir string) error {
	if src == "" {
		return nil
	}
	info, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		return err
	}
	dst := filepath.Join(archiveDir, filepath.Base(src))
	return os.Rename(src, dst)
}

func (store resultStore) archiveMatchingResultFile(fp, suffix, wantTime, archiveDir string, unconditional bool) error {
	src := filepath.Join(store.dir, fp+"."+suffix+".json")
	raw, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !unconditional {
		var rec storedResult
		if err := json.Unmarshal(raw, &rec); err != nil {
			return nil
		}
		recTime := rec.Time
		if recTime == "" {
			recTime = rec.RecordedAt
		}
		if canonical, err := canonicalUTCTime(recTime); err == nil {
			recTime = canonical
		}
		if recTime != wantTime {
			return nil
		}
	}
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		return err
	}
	return os.Rename(src, filepath.Join(archiveDir, filepath.Base(src)))
}

func isWithinDir(path, dir string) bool {
	if path == "" || dir == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (store resultStore) enableEpisode(checkID, episode, fp, passAt, failAt string) (cleared bool, err error) {
	lock, err := store.lockCheck(checkID)
	if err != nil {
		return false, err
	}
	defer lock.Close()

	marker, err := store.readDisabled(checkID)
	if err != nil {
		return false, err
	}
	prev, err := store.readEnabled(checkID)
	if err != nil {
		return false, err
	}

	markerMatches := marker != nil && marker.Episode == episode
	archiveDir := store.resolvedDir(checkID, episode)
	if err := store.archiveMatchingResultFile(fp, ResultPass, passAt, archiveDir, markerMatches); err != nil {
		return false, fmt.Errorf("archiving pass record: %w", err)
	}
	if err := store.archiveMatchingResultFile(fp, ResultFail, failAt, archiveDir, markerMatches); err != nil {
		return false, fmt.Errorf("archiving fail record: %w", err)
	}

	epKey := episodeStoreKey(checkID, episode)
	logCandidates := []string{
		filepath.Join(store.disabledDir(), epKey+"."+ResultPass+".log"),
		filepath.Join(store.disabledDir(), epKey+"."+ResultFail+".log"),
	}
	if markerMatches {
		if isWithinDir(marker.PassLogRef, store.disabledDir()) {
			logCandidates = append(logCandidates, marker.PassLogRef)
		}
		if isWithinDir(marker.FailLogRef, store.disabledDir()) {
			logCandidates = append(logCandidates, marker.FailLogRef)
		}
	}
	for _, candidate := range logCandidates {
		if err := moveIntoArchiveIfPresent(candidate, archiveDir); err != nil {
			return false, fmt.Errorf("archiving copied log %s: %w", candidate, err)
		}
	}

	var episodes []string
	if prev != nil {
		episodes = append(episodes, prev.Episodes...)
	}
	if !slices.Contains(episodes, episode) {
		episodes = append(episodes, episode)
	}
	enabledAt := time.Now().UTC().Format(time.RFC3339Nano)
	if prev != nil && prev.Episode == episode && prev.EnabledAt != "" {
		enabledAt = prev.EnabledAt
	}
	next := enabledState{
		CheckID:     checkID,
		Episode:     episode,
		Fingerprint: fp,
		PassAt:      passAt,
		FailAt:      failAt,
		EnabledAt:   enabledAt,
		Episodes:    episodes,
	}
	if err := publishStoreJSON(store.enabledPath(checkID), next); err != nil {
		return false, err
	}
	if markerMatches {
		if err := os.Remove(store.disabledPath(checkID)); err != nil && !os.IsNotExist(err) {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// enableFace implements `tillr-gate enable`.
func enableFace(args []string) int {
	flags := flag.NewFlagSet("enable", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	check := flags.String("check", "", "the disabled check id to enable (required)")
	episode := flags.String("episode", "", "the resolved episode <fingerprint>|<pass_at>|<fail_at> in RFC3339Nano UTC (required)")
	worktree := flags.String("worktree", ".", "a worktree of the repository that owns the result store")
	storeDir := flags.String("store", "", "override the result store directory")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "Usage: tillr-gate enable --check <check-id> --episode <episode> [--worktree <dir>] [--store <dir>]")
		flags.PrintDefaults()
	}
	if code, answered := parseFlags(flags, args); answered {
		return code
	}
	if *check == "" || *episode == "" || flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "tillr-gate enable: specify --check <check-id> and --episode <episode> with no positional arguments")
		return record.ExitUsage
	}
	fp, passAt, failAt, err := checkresult.ParseEpisode(*episode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tillr-gate enable: %v\n", err)
		return record.ExitUsage
	}
	var store resultStore
	if *storeDir != "" {
		absStore, absErr := filepath.Abs(*storeDir)
		if absErr != nil {
			fmt.Fprintf(os.Stderr, "tillr-gate enable: %v\n", externalf("resolving --store: %v", absErr))
			return record.ExitExternal
		}
		store = resultStore{dir: absStore}
	} else {
		store, err = openResultStore(*worktree)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tillr-gate enable: %v\n", externalf("opening the result store: %v", err))
			return record.ExitExternal
		}
	}
	cleared, err := store.enableEpisode(*check, *episode, fp, passAt, failAt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tillr-gate enable: %v\n", externalf("enabling %q: %v", *check, err))
		return record.ExitExternal
	}
	if cleared {
		fmt.Printf("%s: enabled; contradicting records moved to resolved/\n", *check)
	} else {
		fmt.Printf("%s: episode recorded; marker unchanged\n", *check)
	}
	return record.ExitOK
}
