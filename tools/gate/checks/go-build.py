#!/usr/bin/env python3
"""Build the Go tools and compile their tests concurrently without running cases."""
from concurrent.futures import ThreadPoolExecutor
import os
from pathlib import Path
import subprocess
import sys
import tempfile

GO_TOOL_DIRS = (
    "tools/pi-profiler", "tools/tillr", "tools/tillr-store", "tools/tillr-item",
    "tools/tillr-run", "tools/tillr-gate", "tools/tillr-view", "tools/tillr-land",
    "tools/tillr-drain", "tools/tillr-cli",
)


def build_module(module, scratch):
    env = dict(os.environ, CGO_ENABLED="0")
    env.pop("LAND_BATCH_LIMIT", None)
    # -trimpath, for this check only. The check builds to prove the tree compiles and
    # throws the output away, so trimmed source paths change nothing that is kept -- and
    # without it Go keys compiled packages by their absolute source path, so every new
    # checkout (a new lane's worktree) compiles cold: 2.85 s alone against 0.84 s trimmed,
    # measured 2026-11-16, and past the 9 s ceiling beside nine other gates. It rides in
    # GOFLAGS, after the caller's own, so the `go build` a test binary's TestMain starts
    # is trimmed too. It is NOT a global flag: recipes that build binaries people run
    # (`just tillr-build` and the like) keep their full paths. CGO is off above, so no C
    # object embeds a path, and no Go source here embeds one.
    env["GOFLAGS"] = " ".join(filter(None, [os.environ.get("GOFLAGS", ""), "-trimpath"]))
    # Leave GOCACHE and GOMODCACHE exactly as the caller selected them.
    with tempfile.TemporaryDirectory(dir=scratch, prefix="gate-go-build-") as output:
        listed = subprocess.run(["go", "list", "-f", '{{if eq .Name "main"}}M{{end}}', "./..."],
                                cwd=module, env=env, text=True, capture_output=True)
        if listed.returncode:
            return listed.returncode, listed.stdout + listed.stderr
        if "M" in listed.stdout:
            built = subprocess.run(["go", "build", "-o", output, "./..."], cwd=module,
                                   env=env, text=True, capture_output=True)
            if built.returncode:
                return built.returncode, built.stdout + built.stderr
        compiled = subprocess.run(["go", "test", "-run", "^$", "./..."], cwd=module,
                                  env=env, text=True, capture_output=True)
        return compiled.returncode, compiled.stdout + compiled.stderr


def main():
    try:
        scratch = os.environ["YOLO_DURABLE_DIR"]
        modules = []
        for directory in GO_TOOL_DIRS:
            root = Path(directory)
            if not (root / "go.mod").is_file():
                raise ValueError(f"{directory}/go.mod is missing")
            modules.extend(sorted(path.parent for path in root.rglob("go.mod")))
        with ThreadPoolExecutor(max_workers=len(modules)) as pool:
            results = list(pool.map(lambda module: build_module(module, scratch), modules))
        failed = False
        for module, (code, output) in zip(modules, results):
            print(f"go-build: {module}: {'FAILED' if code else 'built and test files compiled'}")
            if code:
                failed = True
                print(output, end="\n" if not output.endswith("\n") else "")
        return 1 if failed else 0
    except (KeyError, OSError, ValueError, subprocess.SubprocessError) as error:
        print(f"go-build: could not judge: {error}", file=sys.stderr)
        return 4


if __name__ == "__main__":
    sys.exit(main())
