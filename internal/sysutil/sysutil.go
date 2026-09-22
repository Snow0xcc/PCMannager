// Package sysutil wraps the OS services GoBox needs: elevated execution,
// autostart registration, single-instance locking, notifications and external
// command execution.
//
// The portable parts live here; anything Windows-specific is behind build tags.
package sysutil

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Result is the outcome of running an external command.
type Result struct {
	ExitCode int           `json:"exit_code"`
	Stdout   string        `json:"stdout"`
	Stderr   string        `json:"stderr"`
	Duration time.Duration `json:"duration_ms"`
}

// RunOptions configures command execution.
type RunOptions struct {
	// Dir is the working directory ("" = inherit).
	Dir string
	// Shell wraps the command in the platform shell (cmd /c, sh -c), which is
	// required for builtins like `start`, redirection and pipes.
	Shell bool
	// Env appends KEY=VALUE entries to the inherited environment.
	Env []string
	// Stream receives each output line as it is produced (install progress).
	Stream func(line string)
	// Timeout aborts the command after this duration (0 = no limit).
	Timeout time.Duration
}

// Run executes a command and captures its output.
//
// When opts.Stream is set, both stdout and stderr are streamed line by line so
// long-running installers show live progress; the full output is still returned.
func Run(ctx context.Context, command string, opts RunOptions) (*Result, error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	argv := shellArgv(command, opts.Shell)
	if len(argv) == 0 {
		return nil, fmt.Errorf("sysutil: 空命令")
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = opts.Dir
	if len(opts.Env) > 0 {
		cmd.Env = append(cmd.Environ(), opts.Env...)
	}
	hideWindow(cmd) // suppress the console flash on Windows

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动命令失败: %w", err)
	}

	var outBuf, errBuf strings.Builder
	done := make(chan struct{}, 2)

	consume := func(r io.Reader, sink *strings.Builder) {
		defer func() { done <- struct{}{} }()
		sc := bufio.NewScanner(r)
		// Installers can emit very long lines (progress bars); raise the cap.
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			sink.WriteString(line)
			sink.WriteByte('\n')
			if opts.Stream != nil {
				opts.Stream(line)
			}
		}
	}
	go consume(stdout, &outBuf)
	go consume(stderr, &errBuf)

	<-done
	<-done
	waitErr := cmd.Wait()

	res := &Result{
		Stdout:   outBuf.String(),
		Stderr:   errBuf.String(),
		Duration: time.Since(start),
	}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	if waitErr != nil && res.ExitCode == 0 {
		res.ExitCode = -1
	}
	return res, nil
}

// shellArgv builds the argv for a command, optionally through a shell.
func shellArgv(command string, useShell bool) []string {
	command = strings.TrimSpace(command)
	if !useShell {
		return splitArgs(command)
	}
	if runtime.GOOS == "windows" {
		return []string{"cmd.exe", "/c", command}
	}
	return []string{"/bin/sh", "-c", command}
}

// splitArgs splits a command line on spaces, honouring double quotes.
func splitArgs(s string) []string {
	var (
		args    []string
		cur     strings.Builder
		inQuote bool
	)
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
		case r == ' ' && !inQuote:
			if cur.Len() > 0 {
				args = append(args, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		args = append(args, cur.String())
	}
	return args
}

// Which reports whether an executable is discoverable on PATH.
func Which(name string) (string, bool) {
	p, err := exec.LookPath(name)
	return p, err == nil
}

// HasAny reports whether any of the named executables exists on PATH.
func HasAny(names ...string) bool {
	for _, n := range names {
		if _, ok := Which(n); ok {
			return true
		}
	}
	return false
}
