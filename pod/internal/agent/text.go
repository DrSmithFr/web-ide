package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// NormalizeQuestions keeps the valid questions of an ask_user call (MaxQuestions at most,
// 6 options each).
func NormalizeQuestions(raw json.RawMessage) []Question {
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		return nil
	}
	var out []Question
	for _, item := range list {
		if len(out) == MaxQuestions {
			break
		}
		var q struct {
			Question string            `json:"question"`
			Header   string            `json:"header"`
			Options  []json.RawMessage `json:"options"`
			Multiple bool              `json:"multiple"`
		}
		if json.Unmarshal(item, &q) != nil || strings.TrimSpace(q.Question) == "" {
			continue
		}
		nq := Question{Question: q.Question, Header: q.Header, Multiple: q.Multiple, Options: []Option{}}
		if r := []rune(nq.Header); len(r) > 24 {
			nq.Header = string(r[:24])
		}
		for _, o := range q.Options {
			var opt Option
			if json.Unmarshal(o, &opt.Label) != nil {
				_ = json.Unmarshal(o, &opt)
			}
			if strings.TrimSpace(opt.Label) != "" && len(nq.Options) < 6 {
				nq.Options = append(nq.Options, opt)
			}
		}
		out = append(out, nq)
	}
	return out
}

// AnswersText is the text given back to the model for the answers of the user.
func AnswersText(qs []Question, answers [][]string) string {
	var b strings.Builder
	b.WriteString("Answers of the user:")
	for i, q := range qs {
		var given []string
		if i < len(answers) {
			for _, a := range answers[i] {
				if strings.TrimSpace(a) != "" {
					given = append(given, a)
				}
			}
		}
		ans := strings.Join(given, " ; ")
		if ans == "" {
			ans = "(no answer)"
		}
		fmt.Fprintf(&b, "\n%d. %s\n   → %s", i+1, q.Question, ans)
	}
	return b.String()
}

var (
	oscSeq    = regexp.MustCompile("\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)")
	csiSeq    = regexp.MustCompile("\x1b\\[[0-?]*[ -/]*[@-~]")
	escSeq    = regexp.MustCompile("\x1b[()][A-Za-z0-9]|\x1b[=>78NOM]")
	trailingS = regexp.MustCompile(`\s+$`)
)

// PlainOutput is a terminal output as plain text: escape sequences removed, carriage returns
// applied.
func PlainOutput(raw string) string {
	text := escSeq.ReplaceAllString(csiSeq.ReplaceAllString(oscSeq.ReplaceAllString(raw, ""), ""), "")
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
