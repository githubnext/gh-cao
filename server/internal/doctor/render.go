package doctor

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/logger"
)

var renderLog = logger.New("cao:doctor:render")

// renderFormat is a resolved, validated rendering format. Keeping it as a
// distinct type from the raw flag string means a caller cannot pass an
// unvalidated value straight to the text/JSON dispatch below.
type renderFormat string

const (
	renderFormatText renderFormat = "text"
	renderFormatJSON renderFormat = "json"
)

// resolveRenderFormat normalizes and validates the requested report format.
// An empty or "text" value resolves to the default text rendering; "json"
// resolves to the JSON rendering; anything else is rejected so a typo in the
// --format flag fails fast rather than silently falling back to text.
func resolveRenderFormat(format string) (renderFormat, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "text":
		return renderFormatText, nil
	case "json":
		return renderFormatJSON, nil
	default:
		return "", fmt.Errorf("unknown report format %q; expected text or json", format)
	}
}

// Render writes the report in the requested format.
//
// Both formats carry the same check identifiers and the same facts. The text
// format is the default because it is readable by a person and still
// line-oriented enough to grep; the JSON format is for a program that wants
// the whole structure.
func Render(writer io.Writer, report Report, format string) error {
	resolved, err := resolveRenderFormat(format)
	if err != nil {
		renderLog.Printf("report render rejected unknown format")
		return err
	}
	renderLog.Printf("report render format=%s status=%s checks=%d", resolved, report.Summary.Status, report.Summary.Total)
	switch resolved {
	case renderFormatJSON:
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	default:
		return renderText(writer, report)
	}
}

// marker is the fixed-width status marker. It is uppercase and bracketed so a
// reader can scan the left margin and a script can match on it.
func marker(status Status) string {
	switch status {
	case StatusPass:
		return "[ PASS ]"
	case StatusWarn:
		return "[ WARN ]"
	case StatusFail:
		return "[ FAIL ]"
	case StatusSkip:
		return "[ SKIP ]"
	default:
		return "[ ???? ]"
	}
}

func renderText(writer io.Writer, report Report) error {
	buffer := &strings.Builder{}
	fmt.Fprintf(buffer, "CAO server check-up\n")
	fmt.Fprintf(buffer, "  generated   %s\n", report.GeneratedAt)
	fmt.Fprintf(buffer, "  version     %s\n", report.Version)
	fmt.Fprintf(buffer, "  profile     %s\n", report.Profile)
	fmt.Fprintf(buffer, "  namespace   %s\n", report.Namespace)
	fmt.Fprintf(buffer, "  depth       %s\n", depthLabel(report.Deep))

	area := ""
	for _, check := range report.Checks {
		if check.Area != area {
			area = check.Area
			fmt.Fprintf(buffer, "\n%s\n", strings.ToUpper(area))
		}
		fmt.Fprintf(buffer, "  %s %-28s %s\n", marker(check.Status), check.ID, check.Summary)
		for _, item := range check.Details {
			fmt.Fprintf(buffer, "           %-26s %s\n", item.Name, item.Value)
		}
		if check.Remedy != "" {
			fmt.Fprintf(buffer, "           %-26s %s\n", "remedy", check.Remedy)
		}
	}

	fmt.Fprintf(buffer, "\nSUMMARY\n")
	fmt.Fprintf(buffer, "  %s %d checks in %dms: %d pass, %d warn, %d fail, %d skip\n",
		marker(report.Summary.Status), report.Summary.Total, report.Summary.DurationMS,
		report.Summary.Pass, report.Summary.Warn, report.Summary.Fail, report.Summary.Skip)

	// Repeating the problems at the end means the reader never has to scroll
	// back through a long passing report to find what needs attention, and an
	// agent can read only this section.
	problems := make([]Check, 0, report.Summary.Fail+report.Summary.Warn)
	for _, check := range report.Checks {
		if check.Status == StatusFail || check.Status == StatusWarn {
			problems = append(problems, check)
		}
	}
	if len(problems) > 0 {
		fmt.Fprintf(buffer, "\nACTION REQUIRED\n")
		for _, check := range problems {
			fmt.Fprintf(buffer, "  %s %s: %s\n", marker(check.Status), check.ID, check.Summary)
			if check.Remedy != "" {
				fmt.Fprintf(buffer, "           %s\n", check.Remedy)
			}
		}
	}
	_, err := io.WriteString(writer, buffer.String())
	return err
}

func depthLabel(deep bool) string {
	if deep {
		return "deep (includes source read probes)"
	}
	return "standard (no source read probes; pass --deep to include them)"
}
