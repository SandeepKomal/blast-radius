//go:build !windows

package analyze

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A preview whose child process keeps stdout open (like a provider plugin)
// must still return promptly when the deadline passes.
func TestExecRunnerKillsProcessTree(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := ExecRunner(ctx, t.TempDir(), nil, "sh", "-c", "sleep 60 & sleep 60")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want deadline exceeded", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v to give up", d)
	}
}

func TestExecRunnerExitCode(t *testing.T) {
	res, err := ExecRunner(context.Background(), t.TempDir(), []string{"X=42"}, "sh", "-c", `echo "$X $TF_IN_AUTOMATION"; exit 3`)
	if err != nil || res.ExitCode != 3 || string(res.Stdout) != "42 1\n" {
		t.Errorf("res = %+v, err = %v", res, err)
	}
}
