// Copyright © 2026 Groups.io, Inc.
// SPDX-License-Identifier: Apache-2.0

package service

import (
	"regexp"
	"strings"
)

// CrashAnalyzer analyzes log output to determine crash reasons.
type CrashAnalyzer struct {
	panicRe       *regexp.Regexp
	fatalRe       *regexp.Regexp
	logFatalRe    *regexp.Regexp
	oomRe         *regexp.Regexp
	sigTermRe     *regexp.Regexp
	sigKillRe     *regexp.Regexp
	sigIntRe      *regexp.Regexp
	timeoutRe     *regexp.Regexp
	goStackRe     *regexp.Regexp
	goLocRe       *regexp.Regexp
	compilerLocRe *regexp.Regexp
}

// NewCrashAnalyzer creates a new crash analyzer.
func NewCrashAnalyzer() *CrashAnalyzer {
	return &CrashAnalyzer{
		panicRe:       regexp.MustCompile(`(?i)^panic:`),
		fatalRe:       regexp.MustCompile(`(?i)^fatal error:`),
		logFatalRe:    regexp.MustCompile(`(?i)FATAL[:\s]`),
		oomRe:         regexp.MustCompile(`(?i)(out of memory|cannot allocate memory)`),
		sigTermRe:     regexp.MustCompile(`(?i)(signal[:\s]+terminated|SIGTERM|Received signal:\s*SIGTERM)`),
		sigKillRe:     regexp.MustCompile(`(?i)(signal[:\s]+killed|SIGKILL)`),
		sigIntRe:      regexp.MustCompile(`(?i)(signal[:\s]+interrupt|SIGINT)`),
		timeoutRe:     regexp.MustCompile(`(?i)(context deadline exceeded|timeout)`),
		goStackRe:     regexp.MustCompile(`goroutine \d+ \[running\]:`),
		goLocRe:       regexp.MustCompile(`^\s*(/[^\s]+\.go):(\d+)`),
		compilerLocRe: regexp.MustCompile(`([^\s:]+\.go):(\d+):`),
	}
}

// Analyze examines logs and exit code to determine crash reason.
func (a *CrashAnalyzer) Analyze(logs []string, exitCode int) *CrashResult {
	result := &CrashResult{
		ExitCode: exitCode,
	}

	// Clean exit
	if exitCode == 0 && !a.hasCrashIndicators(logs) {
		result.Reason = CrashReasonNone
		return result
	}

	// No logs, analyze exit code
	if len(logs) == 0 {
		return a.analyzeExitCode(result)
	}

	// Check for panic first (highest priority)
	if a.detectPanic(logs, result) {
		return result
	}

	// Check for OOM before fatal (since OOM often appears as "fatal error: out of memory")
	if a.detectOOM(logs, result) {
		return result
	}

	// Check for fatal error
	if a.detectFatal(logs, result) {
		return result
	}

	// Check for signals in logs
	if a.detectSignal(logs, result) {
		return result
	}

	// Check for log.Fatal style
	if a.detectLogFatal(logs, result) {
		return result
	}

	// Check for timeout
	if a.detectTimeout(logs, result) {
		return result
	}

	// Check for generic errors
	if a.detectError(logs, result) {
		return result
	}

	// Fall back to exit code analysis
	// Include last few log lines as context since no specific pattern matched
	a.analyzeExitCode(result)
	if result.Details == "" && len(logs) > 0 {
		// Get last 3 non-empty lines as context
		var lastLines []string
		for i := len(logs) - 1; i >= 0 && len(lastLines) < 3; i-- {
			line := strings.TrimSpace(logs[i])
			if line != "" {
				lastLines = append([]string{line}, lastLines...)
			}
		}
		if len(lastLines) > 0 {
			result.Details = strings.Join(lastLines, " | ")
		}
	}
	return result
}

func (a *CrashAnalyzer) hasCrashIndicators(logs []string) bool {
	for _, line := range logs {
		if a.panicRe.MatchString(line) ||
			a.fatalRe.MatchString(line) ||
			a.oomRe.MatchString(line) ||
			a.sigTermRe.MatchString(line) ||
			a.sigKillRe.MatchString(line) ||
			a.sigIntRe.MatchString(line) {
			return true
		}
	}
	return false
}

func (a *CrashAnalyzer) detectPanic(logs []string, result *CrashResult) bool {
	inStackTrace := false
	var stackLines []string

	for i, line := range logs {
		if a.panicRe.MatchString(line) {
			result.Reason = CrashReasonPanic
			result.Details = strings.TrimPrefix(line, "panic: ")

			// Look for stack trace
			for j := i + 1; j < len(logs); j++ {
				if a.goStackRe.MatchString(logs[j]) {
					inStackTrace = true
				}
				if inStackTrace {
					stackLines = append(stackLines, logs[j])
					// Extract location from stack
					if result.Location == "" {
						if match := a.goLocRe.FindStringSubmatch(logs[j]); match != nil {
							// Extract just filename:line
							parts := strings.Split(match[1], "/")
							result.Location = parts[len(parts)-1] + ":" + match[2]
						}
					}
				}
			}
			result.StackTrace = stackLines
			return true
		}
	}
	return false
}

func (a *CrashAnalyzer) detectFatal(logs []string, result *CrashResult) bool {
	for _, line := range logs {
		if a.fatalRe.MatchString(line) {
			result.Reason = CrashReasonFatal
			result.Details = strings.TrimPrefix(line, "fatal error: ")
			return true
		}
	}
	return false
}

func (a *CrashAnalyzer) detectOOM(logs []string, result *CrashResult) bool {
	for _, line := range logs {
		if a.oomRe.MatchString(line) {
			result.Reason = CrashReasonOOM
			result.Details = "out of memory"
			return true
		}
	}
	return false
}

func (a *CrashAnalyzer) detectSignal(logs []string, result *CrashResult) bool {
	for _, line := range logs {
		if a.sigTermRe.MatchString(line) {
			result.Reason = CrashReasonSignal
			result.Details = "SIGTERM"
			return true
		}
		if a.sigKillRe.MatchString(line) {
			result.Reason = CrashReasonSignal
			result.Details = "SIGKILL"
			return true
		}
		if a.sigIntRe.MatchString(line) {
			result.Reason = CrashReasonSignal
			result.Details = "SIGINT"
			return true
		}
	}
	return false
}

func (a *CrashAnalyzer) detectLogFatal(logs []string, result *CrashResult) bool {
	for _, line := range logs {
		if a.logFatalRe.MatchString(line) {
			result.Reason = CrashReasonLogFatal
			// Extract message after FATAL
			idx := strings.Index(strings.ToUpper(line), "FATAL")
			if idx >= 0 {
				msg := line[idx+5:]
				msg = strings.TrimPrefix(msg, ":")
				msg = strings.TrimSpace(msg)
				result.Details = msg
			}
			return true
		}
	}
	return false
}

func (a *CrashAnalyzer) detectTimeout(logs []string, result *CrashResult) bool {
	for _, line := range logs {
		if a.timeoutRe.MatchString(line) {
			result.Reason = CrashReasonTimeout
			result.Details = line
			return true
		}
	}
	return false
}

func (a *CrashAnalyzer) detectError(logs []string, result *CrashResult) bool {
	errorRe := regexp.MustCompile(`(?i)^error:|: error:`)
	commonErrors := []string{
		"connection refused",
		"address already in use",
		"permission denied",
		"no such file or directory",
		"bind: address already in use",
	}

	for _, line := range logs {
		lineLower := strings.ToLower(line)

		// Check for explicit error prefix
		if errorRe.MatchString(line) {
			result.Reason = CrashReasonError
			result.Details = line
			a.extractLocation(logs, result)
			return true
		}

		// Check for common error patterns
		for _, errPattern := range commonErrors {
			if strings.Contains(lineLower, errPattern) {
				result.Reason = CrashReasonError
				result.Details = line
				return true
			}
		}
	}
	return false
}

func (a *CrashAnalyzer) extractLocation(logs []string, result *CrashResult) {
	for _, line := range logs {
		if match := a.compilerLocRe.FindStringSubmatch(line); match != nil {
			result.Location = match[1] + ":" + match[2]
			return
		}
		if match := a.goLocRe.FindStringSubmatch(line); match != nil {
			parts := strings.Split(match[1], "/")
			result.Location = parts[len(parts)-1] + ":" + match[2]
			return
		}
	}
}

func (a *CrashAnalyzer) analyzeExitCode(result *CrashResult) *CrashResult {
	switch {
	case result.ExitCode == 0:
		result.Reason = CrashReasonNone
	case result.ExitCode >= 128:
		// Exit codes 128+ indicate killed by signal
		// Signal number is exitCode - 128
		result.Reason = CrashReasonSignal
		signalNum := result.ExitCode - 128
		result.Details = signalName(signalNum)
	case result.ExitCode > 0:
		result.Reason = CrashReasonError
		// Details will be populated with log context by caller if available
	default:
		result.Reason = CrashReasonUnknown
	}
	return result
}

func signalName(num int) string {
	switch num {
	case 1:
		return "SIGHUP"
	case 2:
		return "SIGINT"
	case 3:
		return "SIGQUIT"
	case 9:
		return "SIGKILL"
	case 11:
		return "SIGSEGV"
	case 15:
		return "SIGTERM"
	default:
		return "signal"
	}
}
