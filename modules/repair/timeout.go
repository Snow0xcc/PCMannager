package repair

// Execution budget for repair commands.
//
// Every repair action is dispatched through exec.CommandContext so a hung
// process (cleanmgr, DISM, a stalled winget download, ...) can never block
// the caller forever -- the walk panel runs these on its UI thread and the
// HTTP API runs them in request handlers, both of which need a bounded call.
//
// The default budget is 10 minutes, which covers every short diagnostic and
// registry/PowerShell one-liner with generous headroom. A handful of entries
// routinely run longer in the field (full-disk cleanups, Defender scans, DISM
// feature installs, defrag orchestration); those get 15 minutes via the
// longActionTimeouts override, keyed by catalog.go entry id so the catalogue
// data itself stays untouched.

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// defaultActionTimeout is the execution budget for catalogue entries without
// a longActionTimeouts override.
const defaultActionTimeout = 10 * time.Minute

// longActionTimeouts extends the budget for catalogue entries whose real-world
// runtime routinely exceeds the default.
var longActionTimeouts = map[string]time.Duration{
	"defrag":                15 * time.Minute,
	"disk_cleanup":          15 * time.Minute,
	"defender_quick_scan":   15 * time.Minute,
	"wsl_enable_feature":    15 * time.Minute,
	"wsl_enable_vmplatform": 15 * time.Minute,
}

// actionTimeout returns the execution budget for one catalogue action.
func actionTimeout(id string) time.Duration {
	if d, ok := longActionTimeouts[id]; ok {
		return d
	}
	return defaultActionTimeout
}

// runWithContext runs name/args under ctx and returns the trimmed combined
// output. CommandContext terminates the process once ctx is done, which is
// what bounds a hung command; the caller surfaces the deadline as an error.
func runWithContext(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// runActionBudgeted gives one action runner the per-action execution budget:
// it builds the context with the deadline, delegates to start, and rewrites a
// spent budget into a friendly error naming the action id. start must honour
// ctx by returning once it fires (os/exec's CommandContext does exactly that).
func runActionBudgeted(id string, timeout time.Duration, start func(ctx context.Context) (string, error)) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := start(ctx)
	if err == nil {
		return out, nil
	}
	return out, wrapActionError(id, timeout, ctx, err)
}

// wrapActionError converts a spent budget into a friendly, actionable error
// that names the action. os/exec reports a context-killed process as
// "signal: killed" (Linux) or "exit status <n>" (Windows) rather than
// context.DeadlineExceeded, so the check is on the context state, not the
// error chain alone; non-timeout errors pass through untouched.
func wrapActionError(id string, timeout time.Duration, ctx context.Context, err error) error {
	if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("操作 %s 超时：超过 %s 未完成，进程已终止；可稍后重试或手动执行", id, timeout)
	}
	return err
}
