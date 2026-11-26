//go:build linux

package run

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/tillr/store"
)

func init() {
	if os.Getenv("TILLR_TRIAGE_NS_PROBE") == "1" {
		if err := syscall.Mount("", "/", "", syscall.MS_REC|syscall.MS_PRIVATE, ""); err != nil {
			store.FlushSQLiteTraceAtExit()
			os.Exit(1)
		}
		store.FlushSQLiteTraceAtExit()
		os.Exit(0)
	}
	if os.Getenv("TILLR_TRIAGE_NS_EXEC") == "1" {
		if err := runInTriageMountNamespace(); err != nil {
			fmt.Fprintf(os.Stderr, "triage mount namespace setup failed: %v\n", err)
			store.FlushSQLiteTraceAtExit()
			os.Exit(2)
		}
	}
}

func canUseTriageMountNS() bool {
	self, err := os.Executable()
	if err != nil {
		return false
	}
	cmd := exec.Command(self)
	cmd.Env = []string{"TILLR_TRIAGE_NS_PROBE=1"}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Unshareflags: syscall.CLONE_NEWNS,
	}
	return cmd.Run() == nil
}

func prepareTriageExecCmd(cmd *exec.Cmd, adapterPath string, reqArgs []string, root, laneDir, workDir, durableDir, traceDir, storeDir string) (bool, error) {
	if !canUseTriageMountNS() {
		if err := prepareTriageSymlinkView(root, workDir); err != nil {
			return false, err
		}
		return false, nil
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return false, Internalf("creating triage work dir %q: %v", workDir, err)
	}
	self, err := os.Executable()
	if err != nil {
		return false, Internalf("resolving executable for triage mount namespace: %v", err)
	}
	cmd.Path = self
	cmd.Args = append([]string{self}, reqArgs...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid:      true,
		Unshareflags: syscall.CLONE_NEWNS,
	}
	cmd.Env = append(cmd.Env,
		"TILLR_TRIAGE_NS_EXEC=1",
		"TILLR_NS_ADAPTER="+adapterPath,
		"TILLR_NS_ROOT="+root,
		"TILLR_NS_LANE_DIR="+laneDir,
		"TILLR_NS_WORK_DIR="+workDir,
		"TILLR_NS_DURABLE_DIR="+durableDir,
		"TILLR_NS_TRACE_DIR="+traceDir,
		"TILLR_NS_STORE_DIR="+storeDir,
	)
	return true, nil
}

func bindReadOnly(src, dst string) error {
	if err := syscall.Mount(src, dst, "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
		return err
	}
	return syscall.Mount("", dst, "", syscall.MS_BIND|syscall.MS_REMOUNT|syscall.MS_RDONLY|syscall.MS_REC, "")
}

func bindWritable(dir string) error {
	if dir == "" {
		return nil
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil
	}
	if err := syscall.Mount(dir, dir, "", syscall.MS_BIND, ""); err != nil {
		return err
	}
	return syscall.Mount("", dir, "", syscall.MS_BIND|syscall.MS_REMOUNT, "")
}

func runInTriageMountNamespace() error {
	adapter := os.Getenv("TILLR_NS_ADAPTER")
	root := os.Getenv("TILLR_NS_ROOT")
	laneDir := os.Getenv("TILLR_NS_LANE_DIR")
	workDir := os.Getenv("TILLR_NS_WORK_DIR")
	durableDir := os.Getenv("TILLR_NS_DURABLE_DIR")
	traceDir := os.Getenv("TILLR_NS_TRACE_DIR")
	storeDir := os.Getenv("TILLR_NS_STORE_DIR")

	if err := syscall.Mount("", "/", "", syscall.MS_REC|syscall.MS_PRIVATE, ""); err != nil {
		return err
	}
	if root != "" {
		if err := bindReadOnly(root, root); err != nil {
			return err
		}
		if err := bindWritable(storeDir); err != nil {
			return err
		}
		lanesDir := filepath.Join(root, ".tillr", "lanes")
		if info, err := os.Stat(lanesDir); err == nil && info.IsDir() {
			if err := bindReadOnly(lanesDir, lanesDir); err != nil {
				return err
			}
		}
	}
	if err := bindWritable(laneDir); err != nil {
		return err
	}
	if err := bindWritable(durableDir); err != nil {
		return err
	}
	if err := bindWritable(traceDir); err != nil {
		return err
	}
	if root != "" && workDir != "" {
		if err := bindReadOnly(root, workDir); err != nil {
			return err
		}
	}

	var cleanEnv []string
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "TILLR_TRIAGE_NS_") || strings.HasPrefix(entry, "TILLR_NS_") {
			continue
		}
		cleanEnv = append(cleanEnv, entry)
	}
	if workDir != "" {
		if err := os.Chdir(workDir); err != nil {
			return err
		}
	}
	argv := append([]string{adapter}, os.Args[1:]...)
	return syscall.Exec(adapter, argv, cleanEnv)
}
