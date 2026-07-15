//go:build aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris

package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	execTreeRoleEnv      = "CORTEX_EXEC_TREE_ROLE"
	execTreeScenarioEnv  = "CORTEX_EXEC_TREE_SCENARIO"
	execTreeHeartbeatEnv = "CORTEX_EXEC_TREE_HEARTBEAT"
	execTreePIDEnv       = "CORTEX_EXEC_TREE_PID"
	execTreeTestEnv      = "CORTEX_EXEC_TREE_TEST"
)

type execTreeProcessInfo struct {
	PID      int    `json:"pid"`
	Role     string `json:"role"`
	Scenario string `json:"scenario"`
	Test     string `json:"test"`
}

func TestExecRunnerKillsDescendantsAfterWaitDelay(t *testing.T) {
	if runExecTreeHelper(t) {
		return
	}

	heartbeatPath, pidPath := newExecTreePaths(t)
	setExecTreeEnvironment(t, "wait-delay", heartbeatPath, pidPath, "TestExecRunnerKillsDescendantsAfterWaitDelay")

	stdout, stderr, exit, err := (execRunner{}).run(context.Background(), "", os.Args[0], "-test.run=^TestExecRunnerKillsDescendantsAfterWaitDelay$")
	if !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("inherited descriptors error = %v, want exec.ErrWaitDelay; exit=%d stdout=%q stderr=%q", err, exit, stdout, stderr)
	}
	if exit != 0 {
		t.Fatalf("direct helper exit = %d, want 0; stdout=%q stderr=%q", exit, stdout, stderr)
	}
	assertExecTreeStopped(t, heartbeatPath, pidPath, "wait-delay", "TestExecRunnerKillsDescendantsAfterWaitDelay")
}

func TestExecRunnerKillsDescendantsAfterNonzeroExit(t *testing.T) {
	if runExecTreeHelper(t) {
		return
	}

	heartbeatPath, pidPath := newExecTreePaths(t)
	setExecTreeEnvironment(t, "nonzero", heartbeatPath, pidPath, "TestExecRunnerKillsDescendantsAfterNonzeroExit")

	stdout, stderr, exit, err := (execRunner{}).run(context.Background(), "", os.Args[0], "-test.run=^TestExecRunnerKillsDescendantsAfterNonzeroExit$")
	if err != nil {
		t.Fatalf("non-zero helper returned infrastructure error %v; exit=%d stdout=%q stderr=%q", err, exit, stdout, stderr)
	}
	if exit != 1 {
		t.Fatalf("direct helper exit = %d, want 1; stdout=%q stderr=%q", exit, stdout, stderr)
	}
	assertExecTreeStopped(t, heartbeatPath, pidPath, "nonzero", "TestExecRunnerKillsDescendantsAfterNonzeroExit")
}

func TestExecRunnerKillsDescendantsOnCancel(t *testing.T) {
	if runExecTreeHelper(t) {
		return
	}

	heartbeatPath, pidPath := newExecTreePaths(t)
	setExecTreeEnvironment(t, "cancel", heartbeatPath, pidPath, "TestExecRunnerKillsDescendantsOnCancel")

	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan error, 1)
	go func() {
		ready <- waitForExecTreeHeartbeat(heartbeatPath, 5*time.Second)
		cancel()
	}()

	stdout, stderr, exit, err := (execRunner{}).run(ctx, "", os.Args[0], "-test.run=^TestExecRunnerKillsDescendantsOnCancel$")
	if readyErr := <-ready; readyErr != nil {
		t.Fatalf("grandchild readiness: %v; exit=%d stdout=%q stderr=%q", readyErr, exit, stdout, stderr)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled process tree error = %v, want context.Canceled; exit=%d stdout=%q stderr=%q", err, exit, stdout, stderr)
	}
	assertExecTreeStopped(t, heartbeatPath, pidPath, "cancel", "TestExecRunnerKillsDescendantsOnCancel")
}

func TestExecRunnerPreservesDeadlineExceeded(t *testing.T) {
	if os.Getenv("CORTEX_EXEC_DEADLINE_HELPER") == "1" {
		time.Sleep(30 * time.Second)
		return
	}

	t.Setenv("CORTEX_EXEC_DEADLINE_HELPER", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _, _, err := (execRunner{}).run(ctx, "", os.Args[0], "-test.run=^TestExecRunnerPreservesDeadlineExceeded$")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timed-out command error = %v, want context.DeadlineExceeded", err)
	}
}

// runExecTreeHelper implements a three-process test topology:
//
//	test process -> execRunner child -> grandchild heartbeat writer
//
// A heartbeat file is the liveness witness. The helper advances it before the
// direct child exits or cancellation fires, then the outer test verifies that
// it stops advancing after process-group SIGKILL. This works in restricted
// Darwin test environments where binding a Unix listener is not permitted.
func runExecTreeHelper(t *testing.T) bool {
	role := os.Getenv(execTreeRoleEnv)
	if role == "" {
		return false
	}

	heartbeatPath := os.Getenv(execTreeHeartbeatEnv)
	pidPath := os.Getenv(execTreePIDEnv)
	validateExecTreeEnvironment(t, role, heartbeatPath, pidPath)
	switch role {
	case "parent":
		testName := os.Getenv(execTreeTestEnv)
		cmd := exec.Command(os.Args[0], "-test.run=^"+testName+"$")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Env = execTreeEnvironmentWithRole("grandchild")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		scenario := os.Getenv(execTreeScenarioEnv)
		if scenario == "wait-delay" || scenario == "nonzero" {
			if err := waitForExecTreeHeartbeat(heartbeatPath, 5*time.Second); err != nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
				t.Fatalf("grandchild did not become ready: %v", err)
			}
			if scenario == "nonzero" {
				t.Error("intentional non-zero direct-child exit")
			}
			return true
		}
		time.Sleep(30 * time.Second)
		return true

	case "grandchild":
		info := execTreeProcessInfo{
			PID: os.Getpid(), Role: role,
			Scenario: os.Getenv(execTreeScenarioEnv), Test: os.Getenv(execTreeTestEnv),
		}
		data, err := json.Marshal(info)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(pidPath, data, 0o600); err != nil {
			t.Fatal(err)
		}
		for sequence := 1; ; sequence++ {
			if err := os.WriteFile(heartbeatPath, []byte(strconv.Itoa(sequence)), 0o600); err != nil {
				t.Fatal(err)
			}
			time.Sleep(20 * time.Millisecond)
		}

	default:
		t.Fatalf("unknown exec tree helper role %q", role)
		return true
	}
}

func newExecTreePaths(t *testing.T) (heartbeatPath, pidPath string) {
	t.Helper()
	file, err := os.CreateTemp("", "cxet-")
	if err != nil {
		t.Fatal(err)
	}
	base := file.Name()
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(base); err != nil {
		t.Fatal(err)
	}
	heartbeatPath = base + ".heartbeat"
	pidPath = base + ".pid"
	t.Cleanup(func() {
		if info, err := readExecTreeProcessInfo(pidPath); err == nil {
			_ = syscall.Kill(info.PID, syscall.SIGKILL)
		}
		_ = os.Remove(heartbeatPath)
		_ = os.Remove(pidPath)
	})
	return heartbeatPath, pidPath
}

func setExecTreeEnvironment(t *testing.T, scenario, heartbeatPath, pidPath, testName string) {
	t.Helper()
	t.Setenv(execTreeRoleEnv, "parent")
	t.Setenv(execTreeScenarioEnv, scenario)
	t.Setenv(execTreeHeartbeatEnv, heartbeatPath)
	t.Setenv(execTreePIDEnv, pidPath)
	t.Setenv(execTreeTestEnv, testName)
}

func validateExecTreeEnvironment(t *testing.T, role, heartbeatPath, pidPath string) {
	t.Helper()
	if role != "parent" && role != "grandchild" {
		t.Fatalf("invalid exec tree role %q", role)
	}
	if scenario := os.Getenv(execTreeScenarioEnv); scenario != "wait-delay" && scenario != "nonzero" && scenario != "cancel" {
		t.Fatalf("invalid exec tree scenario %q", scenario)
	}
	if heartbeatPath == "" || pidPath == "" || os.Getenv(execTreeTestEnv) == "" {
		t.Fatalf("incomplete exec tree environment: heartbeat=%q pid=%q test=%q", heartbeatPath, pidPath, os.Getenv(execTreeTestEnv))
	}
}

func execTreeEnvironmentWithRole(role string) []string {
	prefix := execTreeRoleEnv + "="
	environment := make([]string, 0, len(os.Environ())+1)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, prefix) {
			environment = append(environment, value)
		}
	}
	return append(environment, prefix+role)
}

func assertExecTreeStopped(t *testing.T, heartbeatPath, pidPath, scenario, testName string) {
	t.Helper()
	info, err := readExecTreeProcessInfo(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Role != "grandchild" || info.Scenario != scenario || info.Test != testName {
		t.Fatalf("grandchild environment mismatch: %+v", info)
	}
	if err := waitForExecTreeHeartbeatStop(heartbeatPath, 2*time.Second); err != nil {
		t.Fatalf("grandchild pid %d survived process-tree termination: %v", info.PID, err)
	}
	// Prevent cleanup from signaling a PID that could be reused after the
	// successfully terminated grandchild has been reaped.
	if err := os.Remove(pidPath); err != nil {
		t.Fatal(err)
	}
}

func readExecTreeProcessInfo(path string) (execTreeProcessInfo, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return execTreeProcessInfo{}, fmt.Errorf("read grandchild process info: %w", err)
	}
	var info execTreeProcessInfo
	if err := json.Unmarshal(contents, &info); err != nil || info.PID <= 0 {
		return execTreeProcessInfo{}, fmt.Errorf("parse grandchild process info %q", contents)
	}
	return info, nil
}

func waitForExecTreeHeartbeat(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	previous := ""
	for {
		contents, err := os.ReadFile(path)
		if err == nil {
			current := strings.TrimSpace(string(contents))
			if previous != "" && current != "" && current != previous {
				return nil
			}
			previous = current
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("heartbeat %q did not advance", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForExecTreeHeartbeatStop(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	stableSince := time.Now()
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	previous := string(contents)
	for {
		time.Sleep(20 * time.Millisecond)
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		current := string(contents)
		if current != previous {
			previous = current
			stableSince = time.Now()
		}
		if time.Since(stableSince) >= 300*time.Millisecond {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("heartbeat %q continued advancing", path)
		}
	}
}
