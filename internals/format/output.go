package format

import (
	"encoding/json"
	"fmt"
	"strings"
)

type OutputFormat string

const (
	Text OutputFormat = "text"
	JSON OutputFormat = "json"
)

func (f OutputFormat) String() string {
	return string(f)
}

var SupportedFormats = []string{
	string(Text),
	string(JSON),
}

func Parse(s string) (OutputFormat, error) {
	s = strings.ToLower(strings.TrimSpace(s))

	switch s {
	case string(Text):
		return Text, nil
	case string(JSON):
		return JSON, nil
	default:
		return "", fmt.Errorf("invalid format: %s", s)
	}
}

func IsValid(s string) bool {
	_, err := Parse(s)
	return err == nil
}

func GetHelpText() string {
	return fmt.Sprintf(`Supported output formats:
- %s: Plain text output (default)
- %s: Output wrapped in a JSON object`,
		Text, JSON)
}

func FormatOutput(content string, formatStr string) string {
	format, err := Parse(formatStr)
	if err != nil {
		return content
	}

	switch format {
	case JSON:
		return formatAsJSON(content)
	case Text:
		fallthrough
	default:
		return content
	}
}

func formatAsJSON(content string) string {
	response := struct {
		Response string `json:"response"`
	}{
		Response: content,
	}

	jsonBytes, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		jsonEscaped := strings.Replace(content, "\\", "\\\\", -1)
		jsonEscaped = strings.Replace(jsonEscaped, "\"", "\\\"", -1)
		jsonEscaped = strings.Replace(jsonEscaped, "\n", "\\n", -1)
		jsonEscaped = strings.Replace(jsonEscaped, "\r", "\\r", -1)
		jsonEscaped = strings.Replace(jsonEscaped, "\t", "\\t", -1)

		return fmt.Sprintf("{\n  \"response\": \"%s\"\n}", jsonEscaped)
	}

	return string(jsonBytes)
}
