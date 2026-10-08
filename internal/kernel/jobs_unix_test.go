//go:build unix

package kernel

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestProcessStartTimeOfSelfAndChild(t *testing.T) {
	ctx := context.Background()
	got, ok := processStartTime(ctx, os.Getpid())
	if !ok {
		t.Skip("process start time unavailable on this host")
	}
	if got.After(time.Now().Add(2*time.Second)) || time.Since(got) > 366*24*time.Hour {
		t.Errorf("implausible own start time %v", got)
	}

	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = detachedAttr()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	child, ok := processStartTime(ctx, cmd.Process.Pid)
	if !ok {
		t.Fatal("child start time should be readable")
	}
	if d := time.Since(child); d < -2*time.Second || d > time.Minute {
		t.Errorf("child start %v is not recent", child)
	}
	if child.Before(got.Add(-2 * time.Second)) {
		t.Errorf("child started before this test process: child=%v self=%v", child, got)
	}
}

func TestTerminateProcessSignalsDetachedGroup(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 30 & wait")
	cmd.SysProcAttr = detachedAttr()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	time.Sleep(100 * time.Millisecond)
	if err := terminateProcess(cmd.Process.Pid); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("detached worker did not stop after SIGTERM")
	}
}

func TestTerminateProcessRefusesInitPID(t *testing.T) {
	if err := terminateProcess(1); err == nil {
		t.Fatal("pid 1 must never be signaled")
	}
}
