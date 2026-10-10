package agent

import (
	"regexp"
	"strings"
)

var (
	oscSeq    = regexp.MustCompile("\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)")
	csiSeq    = regexp.MustCompile("\x1b\\[[0-?]*[ -/]*[@-~]")
	escSeq    = regexp.MustCompile("\x1b[()][A-Za-z0-9]|\x1b[=>78NOM]")
	trailingS = regexp.MustCompile(`\s+$`)
	sgrSeq    = regexp.MustCompile("^\x1b\\[[0-9;]*m$")
)

// PlainOutput is a terminal output as plain text: escape sequences removed, carriage returns
// applied.
func PlainOutput(raw string) string {
	return applyCR(escSeq.ReplaceAllString(csiSeq.ReplaceAllString(oscSeq.ReplaceAllString(raw, ""), ""), ""))
}

// ColorOutput is a terminal output for the page: only its colors (SGR sequences) kept,
// carriage returns applied. Empty when it has no color: the plain output is enough.
func ColorOutput(raw string) string {
	text := oscSeq.ReplaceAllString(raw, "")
	text = csiSeq.ReplaceAllStringFunc(text, func(seq string) string {
		if sgrSeq.MatchString(seq) {
			return seq
		}
		return ""
	})
	text = escSeq.ReplaceAllString(text, "")
	if !strings.Contains(text, "\x1b[") {
		return ""
	}
	return applyCR(text)
}

// applyCR keeps, on each line, what was written after its last carriage return.
func applyCR(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if strings.Contains(l, "\r") {
			trimmed := strings.TrimSuffix(l, "\r")
			if j := strings.LastIndex(trimmed, "\r"); j >= 0 {
				trimmed = trimmed[j+1:]
			}
			lines[i] = trimmed
		}
	}
	return strings.Join(lines, "\n")
}

// Tail keeps the last lines of a text (and at most chars characters).
func Tail(text string, lines, chars int) string {
	t := trailingS.ReplaceAllString(text, "")
	cut := false
	if all := strings.Split(t, "\n"); len(all) > lines {
		t = strings.Join(all[len(all)-lines:], "\n")
		cut = true
	}
	if len(t) > chars {
		t = t[len(t)-chars:]
		cut = true
	}
	if cut {
		return "… (start cut)\n" + t
	}
	return t
}

// TrimEnd removes the blanks at the end of a text.
func TrimEnd(s string) string { return trailingS.ReplaceAllString(s, "") }
