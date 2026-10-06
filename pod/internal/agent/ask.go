package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Question types of ask_user (choice when absent), and the answers the user can give to any
// question instead of an option (the page sends these English words).
var QuestionTypes = []string{"choice", "idea", "compare", "rank", "scenario"}

const (
	DontKnow = "I don't know"
	UpToYou  = "Up to you"
)

// NormalizeQuestions reads the questions of an ask_user call (MaxQuestions at most). A
// question of a new type that breaks its rule (compare without exactly two options, a rank
// out of bounds…) refuses the whole call with an error naming it, so that the model asks
// again; a question without type is kept as before.
func NormalizeQuestions(raw json.RawMessage) ([]Question, error) {
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		return nil, fmt.Errorf("questions must be a list of questions (question, type, options…)")
	}
	var out []Question
	for _, item := range list {
		if len(out) == MaxQuestions {
			break
		}
		var q struct {
			Question  string            `json:"question"`
			Header    string            `json:"header"`
			Type      string            `json:"type"`
			Situation string            `json:"situation"`
			Top       int               `json:"top"`
			Options   []json.RawMessage `json:"options"`
			Multiple  bool              `json:"multiple"`
		}
		if json.Unmarshal(item, &q) != nil || strings.TrimSpace(q.Question) == "" {
			continue
		}
		typ := strings.ToLower(strings.TrimSpace(q.Type))
		switch typ {
		case "", "choice":
			typ = ""
		case "idea", "compare", "rank", "scenario":
		default:
			return nil, fmt.Errorf("question %q: unknown type %q (choice, idea, compare, rank or scenario)", q.Question, q.Type)
		}
		nq := Question{Question: cut(q.Question, 400), Header: cut(q.Header, 24), Type: typ, Multiple: q.Multiple && typ == "", Options: []Option{}}
		for _, o := range q.Options {
			var opt Option
			if json.Unmarshal(o, &opt.Label) != nil {
				_ = json.Unmarshal(o, &opt)
			}
			opt.Label, opt.Description = cut(strings.TrimSpace(opt.Label), 60), cut(opt.Description, 160)
			opt.Pros, opt.Cons = points(opt.Pros), points(opt.Cons)
			if opt.Label != "" && len(nq.Options) < 8 {
				nq.Options = append(nq.Options, opt)
			}
		}
		if err := checkQuestion(&nq, q.Situation, q.Top); err != nil {
			return nil, err
		}
		out = append(out, nq)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("1 to %d questions are needed", MaxQuestions)
	}
	return out, nil
}

// checkQuestion applies the rule of the type of a question, and keeps its situation (scenario)
// or its top (rank).
func checkQuestion(q *Question, situation string, top int) error {
	n := len(q.Options)
	switch q.Type {
	case "idea":
		if n > 0 {
			return fmt.Errorf("question %q: type \"idea\" proposes one idea, without options (the proposal is the question)", q.Question)
		}
	case "compare":
		if n != 2 {
			return fmt.Errorf("question %q: type \"compare\" needs exactly 2 options (it has %d)", q.Question, n)
		}
	case "rank":
		if n < 2 {
			return fmt.Errorf("question %q: type \"rank\" needs 2 to 8 options to order (it has %d)", q.Question, n)
		}
		if top >= n {
			return fmt.Errorf("question %q: type \"rank\" needs a top below the number of options (%d)", q.Question, n)
		}
		if top >= 2 {
			q.Top = top
		}
	case "scenario":
		if q.Situation = cut(strings.TrimSpace(situation), 500); q.Situation == "" {
			return fmt.Errorf("question %q: type \"scenario\" needs a \"situation\" (the concrete case)", q.Question)
		}
		if n < 2 {
			return fmt.Errorf("question %q: type \"scenario\" needs at least 2 options (it has %d)", q.Question, n)
		}
	default:
		if len(q.Options) > 6 {
			q.Options = q.Options[:6]
		}
	}
	return nil
}

// AnswersText is the text given back to the model for the answers of the user: per question
// its type, the answers (what to do with "I don't know" and "Up to you"), and the note.
func AnswersText(qs []Question, answers [][]string, notes []string) string {
	var b strings.Builder
	b.WriteString("Answers of the user:")
	for i, q := range qs {
		var given []string
		if i < len(answers) {
			for _, a := range answers[i] {
				switch {
				case strings.TrimSpace(a) == "":
				case a == DontKnow:
					given = append(given, a+" (the user does not know: offer concrete examples or options)")
				case a == UpToYou:
					given = append(given, a+" (left to you: decide and say what you chose)")
				case q.Type == "rank" && q.Top > 0 && strings.HasPrefix(a, "1. "):
					given = append(given, fmt.Sprintf("%s (the top %d only)", a, q.Top))
				default:
					given = append(given, a)
				}
			}
		}
		ans := strings.Join(given, " ; ")
		if ans == "" {
			ans = "(no answer)"
		}
		typ := q.Type
		if typ == "" {
			typ = "choice"
		}
		fmt.Fprintf(&b, "\n%d. [%s] %s\n   → %s", i+1, typ, q.Question, ans)
		if i < len(notes) && strings.TrimSpace(notes[i]) != "" {
			fmt.Fprintf(&b, "\n   note: %s", strings.Join(strings.Fields(notes[i]), " "))
		}
	}
	return b.String()
}

// cut keeps the first n characters of s.
func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// points keeps up to 6 non-empty pros or cons.
func points(list []string) []string {
	var out []string
	for _, p := range list {
		if p = strings.TrimSpace(p); p != "" && len(out) < 6 {
			out = append(out, cut(p, 120))
		}
	}
	return out
}
