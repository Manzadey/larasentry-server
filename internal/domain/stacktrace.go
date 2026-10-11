package domain

import (
	"regexp"
	"strings"
)

var (
	// vendorPathRegex matches vendor directory occurrences in file paths:
	// matches /vendor/, \vendor\, or vendor/ at line/frame start,
	// while avoiding false positives like VendorController.php or App\Models\Vendor.
	vendorPathRegex  = regexp.MustCompile(`(?i)(?:^|[\s/\\"'` + "`" + `])vendor[/\\].*`)
	framePrefixRegex = regexp.MustCompile(`^(?:#\d+|\d+)\s+`)
)

// StackTraceStats contains frame count statistics.
type StackTraceStats struct {
	TotalFrames int
	AppFrames   int
}

// IsVendorTraceLine checks if a given stack trace line or frame belongs to vendor/third-party code.
func IsVendorTraceLine(line string) bool {
	lower := strings.ToLower(line)
	// If it explicitly belongs to the application path, keep it
	if strings.Contains(lower, "/app/") || strings.Contains(lower, "\\app\\") {
		return false
	}
	return vendorPathRegex.MatchString(line)
}

// IsMainTraceLine checks if the line is the PHP execution entry point {main}.
func IsMainTraceLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return trimmed == "{main}" || strings.HasSuffix(trimmed, " {main}")
}

type stackFrame struct {
	Header string
	Lines  []string
}

func parseStackFrames(raw string) []stackFrame {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	rawLines := strings.Split(raw, "\n")
	var frames []stackFrame
	var currentFrame *stackFrame

	for _, line := range rawLines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if framePrefixRegex.MatchString(trimmed) {
			if currentFrame != nil {
				frames = append(frames, *currentFrame)
			}
			currentFrame = &stackFrame{
				Header: trimmed,
				Lines:  []string{line},
			}
		} else {
			if currentFrame != nil {
				currentFrame.Lines = append(currentFrame.Lines, line)
			} else {
				frames = append(frames, stackFrame{
					Header: trimmed,
					Lines:  []string{line},
				})
			}
		}
	}

	if currentFrame != nil {
		frames = append(frames, *currentFrame)
	}

	return frames
}

// FilterVendorStackTrace filters out third-party vendor frames and {main} from a stack trace string,
// returning only the application's own stack frames.
func FilterVendorStackTrace(raw string) string {
	frames := parseStackFrames(raw)
	if len(frames) == 0 {
		return ""
	}

	var filtered []string
	for _, frame := range frames {
		if IsMainTraceLine(frame.Header) {
			continue
		}
		fullText := strings.Join(frame.Lines, "\n")
		if IsVendorTraceLine(fullText) {
			continue
		}
		filtered = append(filtered, fullText)
	}

	return strings.Join(filtered, "\n")
}

// GetStackTraceStats counts total stack frames and application stack frames (excluding vendor and {main}).
func GetStackTraceStats(raw string) StackTraceStats {
	frames := parseStackFrames(raw)
	stats := StackTraceStats{}
	for _, frame := range frames {
		if IsMainTraceLine(frame.Header) {
			continue
		}
		stats.TotalFrames++
		fullText := strings.Join(frame.Lines, "\n")
		if !IsVendorTraceLine(fullText) {
			stats.AppFrames++
		}
	}
	return stats
}

// AppStackTrace returns the stack trace filtered without vendor frames.
func (inc Incident) AppStackTrace() string {
	return FilterVendorStackTrace(inc.StackTrace)
}

// StackTraceStats returns the statistics for total and application stack frames.
func (inc Incident) StackTraceStats() StackTraceStats {
	return GetStackTraceStats(inc.StackTrace)
}
