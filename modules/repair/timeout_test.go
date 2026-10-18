package repair

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// hangHelperEnv turns the test binary into a deliberately hanging helper
// process, so TestRunWithContextKillsHangingProcess can prove that
// runWithContext really terminates the process once the deadline fires.
const hangHelperEnv = "PCM_REPAIR_HANG_HELPER"

// TestHangHelperProcess is only meaningful when the test binary is re-invoked
// as a helper: it hangs for an hour and would only ever end by being killed.
func TestHangHelperProcess(t *testing.T) {
	if os.Getenv(hangHelperEnv) != "1" {
		t.Skip("helper process only (set via " + hangHelperEnv + ")")
	}
	time.Sleep(time.Hour)
}

func TestActionTimeoutDefaultsAndOverrides(t *testing.T) {
	if got := actionTimeout("flush_dns"); got != defaultActionTimeout {
		t.Fatalf("flush_dns budget = %v, want default %v", got, defaultActionTimeout)
	}
	if got := actionTimeout("does_not_exist"); got != defaultActionTimeout {
		t.Fatalf("unknown id budget = %v, want default %v", got, defaultActionTimeout)
	}
	long := time.Duration(15 * time.Minute)
	for _, id := range []string{"defrag", "disk_cleanup", "defender_quick_scan",
		"wsl_enable_feature", "wsl_enable_vmplatform"} {
		if got := actionTimeout(id); got != long {
			t.Fatalf("%s budget = %v, want %v", id, got, long)
		}
	}
}

func TestRunActionBudgetedTimesOutAndNamesAction(t *testing.T) {
	// The fake runner blocks until its context is done, mimicking a hung
	// cleanmgr/defrag. The budget must end the call instead of blocking
	// forever, and the error must name the action id.
	started := time.Now()
	out, err := runActionBudgeted("disk_cleanup", 50*time.Millisecond,
		func(ctx context.Context) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		})
	elapsed := time.Since(started)

	if err == nil {
		t.Fatal("runActionBudgeted returned nil error for a hung runner")
	}
	if !strings.Contains(err.Error(), "disk_cleanup") {
		t.Fatalf("timeout error must contain the action id, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "超时") {
		t.Fatalf("timeout error must mention 超时, got %q", err.Error())
	}
	if out != "" {
		t.Fatalf("output = %q, want empty", out)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("call returned after %v; timeout not enforced", elapsed)
	}
}

func TestRunActionBudgetedPassesSuccessAndPlainErrorsThrough(t *testing.T) {
	out, err := runActionBudgeted("flush_dns", time.Minute,
		func(ctx context.Context) (string, error) {
			if _, ok := ctx.Deadline(); !ok {
				t.Error("runner context has no deadline; budget not wired")
			}
			return " flushed ", nil
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Trimming lives in runWithContext; the budget wrapper passes the runner's
	// output through untouched.
	if out != " flushed " {
		t.Fatalf("output = %q, want %q", out, "flushed")
	}

	boom := errors.New("exit status 1")
	out, err = runActionBudgeted("flush_dns", time.Minute,
		func(ctx context.Context) (string, error) { return "partial", boom })
	if !errors.Is(err, boom) {
		t.Fatalf("plain runner error must pass through unchanged, got %v", err)
	}
	if out != "partial" {
		t.Fatalf("output = %q, want %q", out, "partial")
	}
}

func TestRunActionBudgetedWrapsDeadlineEvenOnKillSignal(t *testing.T) {
	// os/exec reports a killed process as "signal: killed", not as
	// context.DeadlineExceeded. Once the budget is spent the user must get the
	// friendly timeout error regardless of how the runner surfaced the kill.
	// A deadline that already passed makes context.WithDeadline cancel
	// synchronously with DeadlineExceeded, so the check never depends on
	// timer scheduling (a short sleep does, and flakes under load).
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Minute))
	defer cancel()

	err := wrapActionError("defrag", 15*time.Minute, ctx, errors.New("signal: killed"))
	if err == nil || !strings.Contains(err.Error(), "defrag") || !strings.Contains(err.Error(), "超时") {
		t.Fatalf("kill error must be wrapped into a friendly timeout naming the action, got %v", err)
	}

	fresh := context.Background()
	other := errors.New("access denied")
	if got := wrapActionError("flush_dns", time.Minute, fresh, other); got != other {
		t.Fatalf("non-timeout error must pass through unchanged, got %v", got)
	}
}

func TestRunWithContextKillsHangingProcess(t *testing.T) {
	// Re-invoke this test binary as a helper that hangs for an hour and prove
	// the deadline really terminates it. Without a context-aware runner this
	// call would block for that hour instead of returning around the budget.
	t.Setenv(hangHelperEnv, "1")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := time.Now()
	_, err := runWithContext(ctx, os.Args[0], "-test.run=TestHangHelperProcess")
	elapsed := time.Since(started)

	if err == nil {
		t.Fatal("killed helper must surface an error, got nil")
	}
	if elapsed > 30*time.Second {
		t.Fatalf("runWithContext blocked for %v; the process was not killed on deadline", elapsed)
	}
}
