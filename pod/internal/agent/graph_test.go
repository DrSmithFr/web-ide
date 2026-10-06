package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

const graph = `[
	{"question":"Mode?","id":"a","options":[{"label":"Solo","next":"c"},{"label":"Coop","next":"d"}]},
	{"question":"Platform?","options":["PC","Phone"]},
	{"question":"Difficulty?","id":"c","options":[{"label":"Hard","next":"e"},"Easy"]},
	{"question":"How many players?","id":"d","options":["2","4"]},
	{"question":"Permadeath?","id":"e","type":"idea"}
]`

func TestGraphChecks(t *testing.T) {
	qs, err := NormalizeQuestions(json.RawMessage(graph))
	if err != nil || qs[0].Options[0].Next != "c" || qs[0].ID != "a" {
		t.Fatalf("%v %+v", err, qs)
	}
	for raw, want := range map[string]string{
		`[{"question":"A","id":"a","options":[{"label":"x","next":"z"},"y"]}]`:                                                                                                                 `next "z" names no question`,
		`[{"question":"A","id":"a","options":["x","y"]},{"question":"B","id":"a","options":["x","y"]}]`:                                                                                        `the id "a" is given twice`,
		`[{"question":"A","options":[{"label":"x","next":"b"},"y"]},{"question":"B","id":"b","type":"idea","nextYes":"c"},{"question":"C","id":"c","options":[{"label":"x","next":"b"},"y"]}]`: `b → c → b form a cycle`,
	} {
		if _, err := NormalizeQuestions(json.RawMessage(raw)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", raw, err)
		}
	}
}

func TestGraphAnswers(t *testing.T) {
	qs, _ := NormalizeQuestions(json.RawMessage(graph))
	answers := [][]string{{"Solo"}, {"PC"}, {"Hard"}, nil, {"Yes"}}
	got := AnswersText(qs, answers, nil, []int{0, 2, 4, 1}, nil)
	for _, want := range []string{
		"1. [choice] Mode?\n   → Solo",
		"2. [choice] Solo › Difficulty?\n   → Hard",
		"3. [idea] Solo › Hard › Permadeath?\n   → Yes",
		"4. [choice] Platform?",
		`Not asked (branch not taken): "How many players?".`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	off := 2
	got = AnswersText(qs, [][]string{{"Solo"}, nil, {"Medium"}}, nil, []int{0, 2}, &off)
	if !strings.Contains(got, `left the anticipated path at "Difficulty?" with: Medium. Rethink`) || !strings.Contains(got, `Not asked: "Platform?", "How many players?", "Permadeath?".`) {
		t.Errorf("off path:\n%s", got)
	}
}
