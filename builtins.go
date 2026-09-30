package main

import (
	"crypto/sha1" //nolint:gosec // not used for security, only to name a state directory
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// builtinAgentCommands are handled by devops itself; an agents.yaml command
// with the same name aborts `devops agents` and is a `doctor` failure.
var builtinAgentCommands = []string{"init", "status", "wait", "doctor"}

// Exit codes of `devops agents status`.
const (
	statusReady   = 0
	statusFailed  = 1
	statusRunning = 2
	statusNone    = 3
	waitTimeout   = 124
)

// stateRoot holds per-repo bootstrap state. It is a variable so tests can
// point it at a temporary directory.
var stateRoot = "/tmp/devops-agents"

// bootstrapBinary overrides the executable `init` re-invokes (tests only).
var bootstrapBinary = ""

// stateDir returns <stateRoot>/<basename>-<sha1(abs path)[:8]>, the same
// layout the runner image's agent-repo-init helper uses: files `log`, `done`
// (the bootstrap exit code) and `pid`.
func stateDir(repo string) (string, error) {
	abs, err := filepath.Abs(repo)
	if err != nil {
		return "", err
	}
	sum := sha1.Sum([]byte(abs)) //nolint:gosec // see import
	return filepath.Join(stateRoot, filepath.Base(abs)+"-"+hex.EncodeToString(sum[:])[:8]), nil
}

// shadowedBuiltins lists built-in names that agents.yaml also defines.
func shadowedBuiltins(agents []*Module) []string {
	var out []string
	for _, b := range builtinAgentCommands {
		if len(findModulesForCommand(agents, b)) > 0 {
			out = append(out, b)
		}
	}
	return out
}

func isBuiltinAgentCommand(name string) bool {
	for _, b := range builtinAgentCommands {
		if b == name {
			return true
		}
	}
	return false
}

func runAgentBuiltin(name string, args []string) int {
	switch name {
	case "init":
		return runInit(args)
	case "status":
		return runStatus(args)
	case "wait":
		return runWait(args)
	default:
		return runDoctor(args)
	}
}

// repoArg parses the optional trailing <dir> argument (default ".").
func repoArg(fs *flag.FlagSet) (string, error) {
	switch fs.NArg() {
	case 0:
		return ".", nil
	case 1:
		return fs.Arg(0), nil
	default:
		return "", fmt.Errorf("expected at most one directory, got %d", fs.NArg())
	}
}

type bootState struct {
	code    int // valid when state == statusReady or statusFailed
	state   int
	logPath string
}

func readState(dir string) bootState {
	st := bootState{state: statusNone, logPath: filepath.Join(dir, "log")}
	if b, err := os.ReadFile(filepath.Join(dir, "done")); err == nil {
		code, perr := strconv.Atoi(strings.TrimSpace(string(b)))
		if perr != nil {
			code = 1
		}
		st.code = code
		st.state = statusFailed
		if code == 0 {
			st.state = statusReady
		}
		return st
	}
	if b, err := os.ReadFile(filepath.Join(dir, "pid")); err == nil {
		if pid, perr := strconv.Atoi(strings.TrimSpace(string(b))); perr == nil && pid > 0 && syscall.Kill(pid, 0) == nil {
			st.state = statusRunning
		}
	}
	return st
}

func describeState(st bootState) string {
	switch st.state {
	case statusReady:
		return "ready"
	case statusFailed:
		return fmt.Sprintf("failed (exit %d)", st.code)
	case statusRunning:
		return "running"
	default:
		return "not started"
	}
}

// runInit runs the repo's agent `bootstrap` command once, recording its
// output and exit code under the state dir. It is idempotent: a finished
// successful bootstrap or a live one is left alone unless --retry is given.
func runInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	retry := fs.Bool("retry", false, "run bootstrap again even if it already finished")
	trace := fs.Bool("trace", false, "trace every shell command (set -x, also in bash child scripts) into the log")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repo, err := repoArg(fs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "devops: %v\n", err)
		return 2
	}
	dir, err := stateDir(repo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "devops: %v\n", err)
		return 1
	}
	if *trace {
		_ = os.Setenv(traceEnv, "1")
	}

	st := readState(dir)
	if !*retry {
		switch st.state {
		case statusReady:
			fmt.Fprintf(os.Stderr, "devops: %s is already ready (use --retry to rerun)\n", repo)
			return 0
		case statusRunning:
			fmt.Fprintf(os.Stderr, "devops: bootstrap for %s is already running (see %s)\n", repo, st.logPath)
			return 0
		}
	} else if st.state == statusRunning {
		fmt.Fprintf(os.Stderr, "devops: bootstrap for %s is still running; wait for it before retrying\n", repo)
		return 2
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "devops: %v\n", err)
		return 1
	}
	_ = os.Remove(filepath.Join(dir, "done"))
	logFile, err := os.Create(st.logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "devops: %v\n", err)
		return 1
	}
	defer func() { _ = logFile.Close() }()

	code := runBootstrapProcess(repo, io.MultiWriter(os.Stderr, logFile), dir)
	_ = os.Remove(filepath.Join(dir, "pid"))
	if err := writeFileAtomic(filepath.Join(dir, "done"), []byte(strconv.Itoa(code)+"\n")); err != nil {
		fmt.Fprintf(os.Stderr, "devops: %v\n", err)
		return 1
	}
	return code
}

// runBootstrapProcess runs `devops agents bootstrap` in repo as a child
// process (so the Completed marker and error lines land in the log) and
// returns its exit code.
func runBootstrapProcess(repo string, out io.Writer, stateDir string) int {
	self := bootstrapBinary
	if self == "" {
		self = selfPath()
	}
	cmd := exec.Command(self, "agents", "bootstrap")
	cmd.Dir = repo
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.Env = append(os.Environ(), agentEnv+"=1")
	if err := cmd.Start(); err != nil {
		_, _ = fmt.Fprintf(out, "command failed: %v\n", err)
		return 1
	}
	_ = writeFileAtomic(filepath.Join(stateDir, "pid"), []byte(strconv.Itoa(cmd.Process.Pid)+"\n"))
	if err := cmd.Wait(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() > 0 {
			_, _ = fmt.Fprintf(out, "bootstrap exited with code %d\n", ee.ExitCode())
			return ee.ExitCode()
		}
		_, _ = fmt.Fprintf(out, "command failed: %v\n", err)
		return 1
	}
	return 0
}

func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// runStatus prints the bootstrap state. Exit: 0 ready, 1 failed, 2 running,
// 3 not started.
func runStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repo, err := repoArg(fs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "devops: %v\n", err)
		return 2
	}
	dir, err := stateDir(repo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "devops: %v\n", err)
		return 1
	}
	st := readState(dir)
	fmt.Printf("%s: %s\n", repo, describeState(st))
	if st.state != statusNone {
		fmt.Printf("log: %s\n", st.logPath)
	}
	if st.state == statusFailed || st.state == statusNone {
		fmt.Printf("retry: devops agents init --retry %s\n", repo)
	}
	return st.state
}

// runWait blocks until bootstrap finishes. Exit: 0 ready, 1 failed,
// 124 timeout, 3 never started.
func runWait(args []string) int {
	fs := flag.NewFlagSet("wait", flag.ContinueOnError)
	timeout := fs.Duration("timeout", 10*time.Minute, "give up after this long")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	repo, err := repoArg(fs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "devops: %v\n", err)
		return 2
	}
	dir, err := stateDir(repo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "devops: %v\n", err)
		return 1
	}
	deadline := time.Now().Add(*timeout)
	for {
		st := readState(dir)
		switch st.state {
		case statusReady:
			fmt.Printf("%s: ready\n", repo)
			return 0
		case statusFailed:
			fmt.Printf("%s: %s\n", repo, describeState(st))
			printLogTail(st.logPath, 20)
			fmt.Printf("retry: devops agents init --retry %s   (add --trace to log every command)\n", repo)
			return 1
		case statusNone:
			fmt.Printf("%s: bootstrap was never started; run: devops agents init %s\n", repo, repo)
			return statusNone
		}
		if time.Now().After(deadline) {
			fmt.Printf("%s: still running after %s (log: %s)\n", repo, *timeout, st.logPath)
			return waitTimeout
		}
		time.Sleep(time.Second)
	}
}

func printLogTail(path string, n int) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	fmt.Println("--- last log lines ---")
	fmt.Println(strings.Join(lines, "\n"))
	fmt.Println("----------------------")
}
