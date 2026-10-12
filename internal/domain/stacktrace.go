package domain

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
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

// Формат строки кадра PHP getTraceAsString: "#3 /path/File.php(42): Class->method(args)"
// или "#3 [internal function]: Class->method(args)".
var (
	frameLineRegex = regexp.MustCompile(`^#?(\d+)\s+(.*?)(?:\((\d+)\))?:\s+(.*)$`)
	frameCallRegex = regexp.MustCompile(`^([^(]*?)(->|::)([^(]*)`)
)

// StackFrame is one parsed frame of a PHP stack trace.
type StackFrame struct {
	Index  int
	File   string
	Line   int
	Class  string
	Sep    string
	Method string
	Vendor bool
}

// ShortClass returns the class name without its namespace.
func (f StackFrame) ShortClass() string {
	return f.Class[strings.LastIndex(f.Class, `\`)+1:]
}

// FileName returns the file name without its directory.
func (f StackFrame) FileName() string {
	return f.File[strings.LastIndexAny(f.File, `/\`)+1:]
}

// StackGroup is either a single application frame or a run of consecutive vendor frames.
type StackGroup struct {
	Vendor bool
	Frames []StackFrame
}

// CallNode is one box of the call graph: entry point first, the throwing frame last.
type CallNode struct {
	Vendor   bool
	Group    bool
	Class    string
	File     string
	Calls    []string
	Packages []string
	Count    int
	Via      string
}

// ParseFrames parses a raw PHP stack trace into frames, skipping {main}.
func ParseFrames(raw string) []StackFrame {
	var frames []StackFrame
	for _, f := range parseStackFrames(raw) {
		if IsMainTraceLine(f.Header) {
			continue
		}
		frame := StackFrame{Index: len(frames), Vendor: IsVendorTraceLine(strings.Join(f.Lines, "\n"))}
		m := frameLineRegex.FindStringSubmatch(f.Header)
		if m == nil {
			frame.Method = f.Header
			frames = append(frames, frame)
			continue
		}
		frame.Index, _ = strconv.Atoi(m[1])
		frame.File = m[2]
		frame.Line, _ = strconv.Atoi(m[3])
		if c := frameCallRegex.FindStringSubmatch(m[4]); c != nil {
			frame.Class, frame.Sep, frame.Method = c[1], c[2], c[3]
		} else {
			frame.Method = strings.SplitN(m[4], "(", 2)[0]
		}
		frames = append(frames, frame)
	}
	return frames
}

// GroupFrames keeps application frames separate and collapses consecutive vendor frames.
func GroupFrames(frames []StackFrame) []StackGroup {
	var groups []StackGroup
	for _, f := range frames {
		if f.Vendor && len(groups) > 0 && groups[len(groups)-1].Vendor {
			groups[len(groups)-1].Frames = append(groups[len(groups)-1].Frames, f)
			continue
		}
		groups = append(groups, StackGroup{Vendor: f.Vendor, Frames: []StackFrame{f}})
	}
	return groups
}

// BuildCallGraph orders frames from the entry point to the throw site, merging consecutive
// calls of the same class and collapsing vendor runs into one node with their packages.
func BuildCallGraph(frames []StackFrame) []CallNode {
	var nodes []CallNode
	for i := len(frames) - 1; i >= 0; i-- {
		f := frames[i]
		var last *CallNode
		if len(nodes) > 0 {
			last = &nodes[len(nodes)-1]
		}
		if f.Vendor {
			pkg := vendorPackage(f.File)
			if last != nil && last.Group {
				last.Count++
				if pkg != "" && !slices.Contains(last.Packages, pkg) {
					last.Packages = append(last.Packages, pkg)
				}
				continue
			}
			node := CallNode{Vendor: true, Group: true, Count: 1, Via: f.Method}
			if pkg != "" {
				node.Packages = []string{pkg}
			}
			nodes = append(nodes, node)
			continue
		}
		call := f.Method
		if f.Line > 0 {
			call += fmt.Sprintf(":%d", f.Line)
		}
		key := f.Class
		if key == "" {
			key = f.File
		}
		if last != nil && !last.Group && (last.Class == key || (last.Class == "" && last.File == key)) {
			last.Calls = append(last.Calls, call)
			continue
		}
		nodes = append(nodes, CallNode{Class: f.Class, File: f.File, Calls: []string{call}, Via: f.Method})
	}
	return nodes
}

// vendorPackage returns "laravel/framework" for ".../vendor/laravel/framework/src/...".
func vendorPackage(path string) string {
	path = strings.ReplaceAll(path, `\`, "/")
	idx := strings.LastIndex(path, "vendor/")
	if idx < 0 {
		return ""
	}
	parts := strings.SplitN(path[idx+len("vendor/"):], "/", 3)
	if len(parts) < 2 {
		return ""
	}
	return parts[0] + "/" + parts[1]
}
