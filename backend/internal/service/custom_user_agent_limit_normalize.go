package service

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	maxNormalizedUALabelBytes = 255
	maxStoredRawUABytes       = 512
)

var uaVersionTokenRegex = regexp.MustCompile(`^[vV]?\d+(?:[._-]\d+)*(?:[-+][0-9A-Za-z._-]+)?$`)

// NormalizeUserAgentForLimit normalizes a raw User-Agent header into a stable deduplication key
// and display label. It strips client, terminal, and trailing build/originator versions while
// preserving the client name and primary environment block (OS name, OS version, architecture).
func NormalizeUserAgentForLimit(raw string) (key string, label string) {
	collapsed := strings.Join(strings.Fields(strings.TrimSpace(raw)), " ")
	if collapsed == "" {
		return "(empty)", "(empty)"
	}

	prefix := collapsed
	env := ""
	if openIdx := strings.IndexByte(collapsed, '('); openIdx >= 0 {
		prefix = strings.TrimSpace(collapsed[:openIdx])
		rest := collapsed[openIdx+1:]
		if closeIdx := strings.IndexByte(rest, ')'); closeIdx >= 0 {
			env = normalizeUAEnvSegment(rest[:closeIdx])
		} else {
			env = normalizeUAEnvSegment(rest)
		}
	}

	cleanPrefix := stripClientPrefixVersions(prefix)
	switch {
	case cleanPrefix != "" && env != "":
		label = cleanPrefix + " (" + env + ")"
	case cleanPrefix != "":
		label = cleanPrefix
	case env != "":
		label = "(" + env + ")"
	default:
		label = collapsed
	}

	label = truncateUTF8Bytes(label, maxNormalizedUALabelBytes)
	return strings.ToLower(label), label
}

// TruncateRawUserAgent trims whitespace and bounds raw User-Agent strings safely on UTF-8 boundaries.
func TruncateRawUserAgent(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	return truncateUTF8Bytes(trimmed, maxStoredRawUABytes)
}

func normalizeUAEnvSegment(segment string) string {
	parts := strings.Split(segment, ";")
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Join(strings.Fields(strings.TrimSpace(part)), " ")
		if part != "" {
			cleaned = append(cleaned, part)
		}
	}
	return strings.Join(cleaned, "; ")
}

func stripClientPrefixVersions(prefix string) string {
	fields := strings.Fields(prefix)
	if len(fields) == 0 {
		return ""
	}
	out := make([]string, 0, len(fields))
	for _, tok := range fields {
		if slashIdx := strings.IndexByte(tok, '/'); slashIdx > 0 && slashIdx < len(tok)-1 {
			name := tok[:slashIdx]
			ver := tok[slashIdx+1:]
			if isUAVersionToken(ver) {
				out = append(out, name)
				continue
			}
			out = append(out, tok)
			continue
		}
		if len(out) > 0 && isUAVersionToken(tok) {
			continue
		}
		out = append(out, tok)
	}
	if len(out) == 0 {
		return prefix
	}
	return strings.Join(out, " ")
}

func isUAVersionToken(tok string) bool {
	return uaVersionTokenRegex.MatchString(tok)
}

func truncateUTF8Bytes(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
}
