// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/wingedpig/trellis/internal/config"
	"github.com/wingedpig/trellis/internal/events"
	"github.com/wingedpig/trellis/internal/logs"
)

const (
	defaultStopTimeout = 10 * time.Second
	defaultLogBuffer   = 1000
)

// Process manages a single service process.
type Process struct {
	cfg config.ServiceConfig
	bus events.EventBus

	mu            sync.RWMutex
	cmd           *exec.Cmd
	state         ProcessState
	pid           int
	exitCode      int
	startedAt     time.Time
	stoppedAt     time.Time
	logs          *LogBuffer
	stopRequested bool
	parentCtx     context.Context

	onExit    func(int)
	cancelFn  context.CancelFunc
	waitDone  chan struct{}
	isRunning bool
}

// NewProcess creates a new process for the given service config.
func NewProcess(cfg config.ServiceConfig, bus events.EventBus) *Process {
	bufSize := defaultLogBuffer
	if cfg.LogBufferSize > 0 {
		bufSize = cfg.LogBufferSize
	}

	logBuf := NewLogBuffer(bufSize)

	// Configure parser and deriver if logging config is set
	if cfg.Logging.Parser.Type != "" || len(cfg.Logging.Layout) > 0 {
		logBuf.SetParser(cfg.Logging.Parser, cfg.Logging.Derive)
	}

	return &Process{
		cfg:   cfg,
		bus:   bus,
		state: StatusStopped,
		logs:  logBuf,
	}
}

// Start starts the process.
func (p *Process) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.isRunning {
		return fmt.Errorf("process already running")
	}

	cmdArgs := p.cfg.GetCommand()
	if len(cmdArgs) == 0 {
		err := fmt.Errorf("service %s: empty command", p.cfg.Name)
		p.logs.Write(fmt.Sprintf("[trellis] Error: %v", err))
		return err
	}

	// Append configured args to command
	if len(p.cfg.Args) > 0 {
		cmdArgs = append(cmdArgs, p.cfg.Args...)
	}

	// Create a cancellable context
	runCtx, cancel := context.WithCancel(ctx)
	p.cancelFn = cancel
	p.parentCtx = ctx

	// Build command
	cmd := exec.CommandContext(runCtx, cmdArgs[0], cmdArgs[1:]...)
	cmd.Dir = p.cfg.WorkDir

	// Create a new process group so we can kill child processes too
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Set environment
	cmd.Env = os.Environ()
	for k, v := range p.cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	// Capture stdout and stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		p.logs.Write(fmt.Sprintf("[trellis] Error creating stdout pipe: %v", err))
		return fmt.Errorf("stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		p.logs.Write(fmt.Sprintf("[trellis] Error creating stderr pipe: %v", err))
		return fmt.Errorf("stderr pipe: %w", err)
	}

	// Log the command being started
	p.logs.Write(fmt.Sprintf("[trellis] Starting: %v (workdir: %s)", cmdArgs, p.cfg.WorkDir))

	// Start the process
	p.state = StatusStarting
	if err := cmd.Start(); err != nil {
		p.state = StatusStopped
		p.logs.Write(fmt.Sprintf("[trellis] Failed to start: %v", err))
		return fmt.Errorf("start process: %w", err)
	}

	p.cmd = cmd
	p.pid = cmd.Process.Pid
	p.startedAt = time.Now()
	p.exitCode = 0
	p.isRunning = true
	p.state = StatusRunning
	p.waitDone = make(chan struct{})

	// Capture output in background
	go p.captureOutput(stdout)
	go p.captureOutput(stderr)

	// Wait for process in background
	go p.waitForExit()

	return nil
}

// Stop stops the process gracefully.
func (p *Process) Stop(ctx context.Context) error {
	p.mu.Lock()
	if !p.isRunning {
		p.mu.Unlock()
		return nil
	}

	p.state = StatusStopping
	p.stopRequested = true
	cmd := p.cmd
	waitDone := p.waitDone
	p.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return nil
	}

	// Parse stop timeout
	timeout := defaultStopTimeout
	if p.cfg.StopTimeout != "" {
		if d, err := time.ParseDuration(p.cfg.StopTimeout); err == nil {
			timeout = d
		}
	}

	// Send stop signal to the entire process group
	sig := syscall.SIGTERM
	if p.cfg.StopSignal == "SIGKILL" {
		sig = syscall.SIGKILL
	} else if p.cfg.StopSignal == "SIGINT" {
		sig = syscall.SIGINT
	}

	// Signal the process group (negative PID) to kill child processes too
	pgid := cmd.Process.Pid
	syscall.Kill(-pgid, sig)

	// Wait for process to exit or timeout
	select {
	case <-waitDone:
		// Process exited
	case <-time.After(timeout):
		// Force kill the entire process group
		syscall.Kill(-pgid, syscall.SIGKILL)
		<-waitDone
	case <-ctx.Done():
		syscall.Kill(-pgid, syscall.SIGKILL)
		<-waitDone
	}

	return nil
}

// Signal sends a signal to the process.
func (p *Process) Signal(sig string) error {
	p.mu.Lock()
	cmd := p.cmd
	if cmd == nil || cmd.Process == nil {
		p.mu.Unlock()
		return errors.New("process not running")
	}

	var signal os.Signal
	switch sig {
	case "SIGTERM":
		signal = syscall.SIGTERM
		p.stopRequested = true
	case "SIGKILL":
		signal = syscall.SIGKILL
		p.stopRequested = true
	case "SIGINT":
		signal = syscall.SIGINT
		p.stopRequested = true
	case "SIGHUP":
		signal = syscall.SIGHUP
	case "SIGUSR1":
		signal = syscall.SIGUSR1
	case "SIGUSR2":
		signal = syscall.SIGUSR2
	default:
		p.mu.Unlock()
		return fmt.Errorf("unknown signal: %s", sig)
	}
	p.mu.Unlock()

	return cmd.Process.Signal(signal)
}

// Status returns the current process status.
func (p *Process) Status() ServiceStatus {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return ServiceStatus{
		State:     p.state,
		PID:       p.pid,
		ExitCode:  p.exitCode,
		StartedAt: p.startedAt,
		StoppedAt: p.stoppedAt,
	}
}

// Logs returns the last n lines of output.
func (p *Process) Logs(n int) []string {
	return p.logs.Lines(n)
}

// ParsedLogs returns the last n parsed log entries.
func (p *Process) ParsedLogs(n int) []*logs.LogEntry {
	return p.logs.Entries(n)
}

// HasParser returns true if a log parser is configured.
func (p *Process) HasParser() bool {
	return p.logs.HasParser()
}

// LogSize returns the number of lines in the log buffer.
func (p *Process) LogSize() int {
	return p.logs.Size()
}

// ClearLogs clears the log buffer.
func (p *Process) ClearLogs() {
	p.logs.Clear()
}

// SubscribeLogs returns a channel that receives new log lines.
func (p *Process) SubscribeLogs() chan LogLine {
	return p.logs.Subscribe()
}

// UnsubscribeLogs removes a log subscription.
func (p *Process) UnsubscribeLogs(ch chan LogLine) {
	p.logs.Unsubscribe(ch)
}

// CloseLogSubscribers closes all log subscriber channels.
// Used before replacing a process to ensure orphaned subscribers exit cleanly.
func (p *Process) CloseLogSubscribers() {
	p.logs.CloseAllSubscribers()
}

// LogSequence returns the current log sequence number.
func (p *Process) LogSequence() int64 {
	return p.logs.Sequence()
}

// OnExit sets a callback for when the process exits.
func (p *Process) OnExit(fn func(int)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onExit = fn
}

func (p *Process) captureOutput(r io.Reader) {
	br := bufio.NewReader(r)

	for {
		line, err := br.ReadString('\n')
		if len(line) > 0 {
			// Remove trailing newline if present
			line = strings.TrimSuffix(line, "\n")
			line = strings.TrimSuffix(line, "\r")

			// Truncate very long lines (>1MB) to prevent memory issues
			const maxLineLen = 1024 * 1024
			if len(line) > maxLineLen {
				line = line[:maxLineLen] + "... [truncated]"
			}
			p.logs.Write(line)
		}
		if err != nil {
			if err != io.EOF {
				p.logs.Write(fmt.Sprintf("[trellis] Output read error: %v", err))
			}
			break
		}
	}
}

func (p *Process) waitForExit() {
	cmd := p.cmd
	err := cmd.Wait()

	p.mu.Lock()
	p.isRunning = false
	p.stoppedAt = time.Now()

	// Log the exit
	if err != nil {
		p.logs.Write(fmt.Sprintf("[trellis] Process exited with error: %v", err))
	} else {
		p.logs.Write("[trellis] Process exited cleanly")
	}
	wasStopRequested := p.stopRequested

	// Check if parent context was cancelled (external stop request)
	if p.parentCtx != nil && p.parentCtx.Err() != nil {
		wasStopRequested = true
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			p.exitCode = exitErr.ExitCode()
			// If stop was requested, it's a clean stop, not a crash
			if wasStopRequested {
				p.state = StatusStopped
			} else if p.exitCode != 0 {
				p.state = StatusCrashed
			} else {
				p.state = StatusStopped
			}
		} else {
			// If stop was requested, still consider it stopped
			if wasStopRequested {
				p.state = StatusStopped
				p.exitCode = 0
			} else {
				p.state = StatusCrashed
				p.exitCode = -1
			}
		}
	} else {
		p.exitCode = 0
		p.state = StatusStopped
	}

	exitCode := p.exitCode
	onExit := p.onExit
	cancelFn := p.cancelFn
	waitDone := p.waitDone
	p.cmd = nil
	p.pid = 0
	p.stopRequested = false
	p.parentCtx = nil
	p.mu.Unlock()

	// Cancel context
	if cancelFn != nil {
		cancelFn()
	}

	// Signal that wait is done
	close(waitDone)

	// Call exit callback (only if not a requested stop)
	if onExit != nil && !wasStopRequested {
		onExit(exitCode)
	}
}
