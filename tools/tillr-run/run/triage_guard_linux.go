//go:build linux

package run

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/tillr/store"
)

const (
	roMountNSExecEnv   = "TILLR_RO_NS_EXEC"
	roMountNSConfigEnv = "TILLR_RO_NS_CONFIG"
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
	if os.Getenv(roMountNSExecEnv) == "1" {
		if err := runInReadOnlyMountNamespace(); err != nil {
			fmt.Fprintf(os.Stderr, "read-only mount namespace setup failed: %v\n", err)
			store.FlushSQLiteTraceAtExit()
			os.Exit(2)
		}
	}
}

// CanUseReadOnlyMountNS reports whether this process can unshare a private
// Linux mount namespace (CLONE_NEWNS) and mark "/" private.
func CanUseReadOnlyMountNS() bool {
	return canUseTriageMountNS()
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

// PrepareReadOnlyMountCmd rewrites cmd to re-exec the current binary inside a
// private Linux mount namespace (CLONE_NEWNS), apply cfg.Mounts in order,
// chdir to cfg.WorkDir, and exec cfg.TargetPath with cfg.TargetArgs.
// Returns (false, nil) when unprivileged CLONE_NEWNS is unavailable so callers
// can apply a userspace fallback.
func PrepareReadOnlyMountCmd(cmd *exec.Cmd, cfg ReadOnlyMountConfig) (bool, error) {
	if !CanUseReadOnlyMountNS() {
		return false, nil
	}
	if cfg.TargetPath == "" {
		return false, Internalf("read-only mount namespace requires a target executable path")
	}
	self, err := os.Executable()
	if err != nil {
		return false, Internalf("resolving executable for read-only mount namespace: %v", err)
	}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		return false, Internalf("encoding read-only mount namespace config: %v", err)
	}
	cmd.Path = self
	cmd.Args = append([]string{self}, cfg.TargetArgs...)
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Unshareflags = syscall.CLONE_NEWNS
	cmd.Env = append(cmd.Env,
		roMountNSExecEnv+"=1",
		roMountNSConfigEnv+"="+string(encoded),
	)
	return true, nil
}

func prepareTriageExecCmd(cmd *exec.Cmd, adapterPath string, reqArgs []string, root, laneDir, workDir, durableDir, traceDir, storeDir string) (bool, error) {
	if !CanUseReadOnlyMountNS() {
		if err := prepareTriageSymlinkView(root, workDir); err != nil {
			return false, err
		}
		return false, nil
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return false, Internalf("creating triage work dir %q: %v", workDir, err)
	}
	var mounts []MountOp
	if root != "" {
		mounts = append(mounts, MountOp{Src: root, Dst: root, ReadOnly: true})
		if storeDir != "" {
			mounts = append(mounts, MountOp{Src: storeDir, Dst: storeDir, ReadOnly: false})
		}
		lanesDir := filepath.Join(root, ".tillr", "lanes")
		mounts = append(mounts, MountOp{Src: lanesDir, Dst: lanesDir, ReadOnly: true})
	}
	if laneDir != "" {
		mounts = append(mounts, MountOp{Src: laneDir, Dst: laneDir, ReadOnly: false})
	}
	if durableDir != "" {
		mounts = append(mounts, MountOp{Src: durableDir, Dst: durableDir, ReadOnly: false})
	}
	if traceDir != "" {
		mounts = append(mounts, MountOp{Src: traceDir, Dst: traceDir, ReadOnly: false})
	}
	if root != "" && workDir != "" {
		mounts = append(mounts, MountOp{Src: root, Dst: workDir, ReadOnly: true})
	}
	return PrepareReadOnlyMountCmd(cmd, ReadOnlyMountConfig{
		TargetPath: adapterPath,
		TargetArgs: reqArgs,
		WorkDir:    workDir,
		Mounts:     mounts,
	})
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

func runInReadOnlyMountNamespace() error {
	rawCfg := os.Getenv(roMountNSConfigEnv)
	var cfg ReadOnlyMountConfig
	if err := json.Unmarshal([]byte(rawCfg), &cfg); err != nil {
		return fmt.Errorf("decoding mount config: %w", err)
	}
	if err := syscall.Mount("", "/", "", syscall.MS_REC|syscall.MS_PRIVATE, ""); err != nil {
		return err
	}
	for _, m := range cfg.Mounts {
		if m.ReadOnly {
			if m.Src == "" || m.Dst == "" {
				continue
			}
			if m.Src == m.Dst {
				info, err := os.Stat(m.Src)
				if err != nil || !info.IsDir() {
					continue
				}
			}
			if err := bindReadOnly(m.Src, m.Dst); err != nil {
				return fmt.Errorf("bind-mount read-only %q -> %q: %w", m.Src, m.Dst, err)
			}
		} else {
			if err := bindWritable(m.Dst); err != nil {
				return fmt.Errorf("bind-mount writable %q: %w", m.Dst, err)
			}
		}
	}

	var cleanEnv []string
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "TILLR_RO_NS_") || strings.HasPrefix(entry, "TILLR_TRIAGE_NS_") || strings.HasPrefix(entry, "TILLR_NS_") {
			continue
		}
		cleanEnv = append(cleanEnv, entry)
	}
	if cfg.WorkDir != "" {
		if err := os.Chdir(cfg.WorkDir); err != nil {
			return err
		}
	}
	argv := append([]string{cfg.TargetPath}, os.Args[1:]...)
	return syscall.Exec(cfg.TargetPath, argv, cleanEnv)
}
