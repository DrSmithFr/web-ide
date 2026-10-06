package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeQuestions(t *testing.T) {
	qs, err := NormalizeQuestions(json.RawMessage(`[
		{"question":"Format?","options":["CSV",{"label":"JSON","pros":["typed",""," "]}]},
		{"question":"Ship it?","type":"Idea"},
		{"question":"Which?","type":"compare","options":[{"label":"A","cons":["slow"]},{"label":"B"}]},
		{"question":"Order?","type":"rank","top":2,"options":["a","b","c"]},
		{"question":"Empty file?","type":"scenario","situation":" No rows at midnight. ","options":["None","Empty"],"multiple":true},
		{"question":"  "}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(qs) != 5 || qs[0].Type != "" || len(qs[0].Options[1].Pros) != 1 || qs[1].Type != "idea" || qs[2].Options[0].Cons[0] != "slow" ||
		qs[3].Top != 2 || qs[4].Situation != "No rows at midnight." || qs[4].Multiple {
		t.Fatalf("%+v", qs)
	}
	for raw, want := range map[string]string{
		`[{"question":"Q","type":"compare","options":["a","b","c"]}]`:      `"compare" needs exactly 2 options (it has 3)`,
		`[{"question":"Q","type":"idea","options":["a"]}]`:                 `without options`,
		`[{"question":"Q","type":"rank","top":3,"options":["a","b","c"]}]`: `top below`,
		`[{"question":"Q","type":"scenario","options":["a","b"]}]`:         `needs a "situation"`,
		`[{"question":"Q","type":"poll","options":["a","b"]}]`:             `unknown type "poll"`,
		`[]`:               `1 to 10 questions`,
		`{"question":"Q"}`: `must be a list`,
	} {
		if _, err := NormalizeQuestions(json.RawMessage(raw)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", raw, err)
		}
	}
}

func TestAnswersText(t *testing.T) {
	qs := []Question{{Question: "Format?"}, {Question: "Order?", Type: "rank", Top: 2}, {Question: "Ship?", Type: "idea"}, {Question: "Name?"}}
	got := AnswersText(qs, [][]string{{"CSV"}, {"1. b, 2. a"}, {DontKnow}, {UpToYou}}, []string{"", "", "not\nsure", ""}, nil, nil)
	for _, want := range []string{
		"1. [choice] Format?\n   → CSV",
		"→ 1. b, 2. a (the top 2 only)",
		"3. [idea] Ship?\n   → I don't know (the user does not know: offer concrete examples or options)\n   note: not sure",
		"→ Up to you (left to you: decide and say what you chose)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if !strings.Contains(AnswersText(qs[:1], nil, nil, nil, nil), "(no answer)") {
		t.Error("no answer")
	}
}
