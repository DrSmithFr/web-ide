package agent

import (
	"fmt"
	"strings"
)

// A call of ask_user may hold a small graph of questions: an option (for an idea: nextYes /
// nextNo) names the id of the question asked when it is chosen. The questions no option
// leads to are the roots, asked in order; the page walks the graph without the model and
// sends the path taken (web/src/llm/ask.ts walk, which follows the same rules).

// nexts are the ids a question leads to, with the answer that leads there.
func nexts(q Question) [][2]string {
	var out [][2]string
	for _, o := range q.Options {
		if o.Next != "" {
			out = append(out, [2]string{o.Label, o.Next})
		}
	}
	if q.NextYes != "" {
		out = append(out, [2]string{"Yes", q.NextYes}, [2]string{"Exactly", q.NextYes})
	}
	if q.NextNo != "" {
		out = append(out, [2]string{"No", q.NextNo})
	}
	return out
}

// checkGraph refuses duplicate ids, a next naming no question and cycles.
func checkGraph(qs []Question) error {
	index := map[string]int{}
	for i, q := range qs {
		if q.ID == "" {
			continue
		}
		if _, dup := index[q.ID]; dup {
			return fmt.Errorf("questions %q: the id %q is given twice", q.Question, q.ID)
		}
		index[q.ID] = i
	}
	for _, q := range qs {
		for _, n := range nexts(q) {
			if _, ok := index[n[1]]; !ok {
				return fmt.Errorf("question %q: next %q names no question of this call", q.Question, n[1])
			}
		}
	}
	// Depth first: a question met again while on the current branch closes a cycle.
	state := make([]int, len(qs)) // 0 unseen, 1 on the branch, 2 done
	var stack []string
	var visit func(i int) error
	visit = func(i int) error {
		state[i] = 1
		stack = append(stack, qs[i].ID)
		for _, n := range nexts(qs[i]) {
			j := index[n[1]]
			if state[j] == 1 {
				k := 0
				for stack[k] != n[1] {
					k++
				}
				return fmt.Errorf("questions %s → %s form a cycle", strings.Join(stack[k:], " → "), n[1])
			}
			if state[j] == 0 {
				if err := visit(j); err != nil {
					return err
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[i] = 2
		return nil
	}
	for i := range qs {
		if state[i] == 0 {
			if err := visit(i); err != nil {
				return err
			}
		}
	}
	return nil
}

// breadcrumbs gives, per question of the path, the labels chosen to reach it
// ("Coop › 2 players › "), empty for a root.
func breadcrumbs(qs []Question, answers [][]string, path []int) map[int]string {
	out := map[int]string{}
	index := map[string]int{}
	for i, q := range qs {
		if q.ID != "" {
			index[q.ID] = i
		}
	}
	for _, i := range path {
		for _, n := range nexts(qs[i]) {
			j, ok := index[n[1]]
			if !ok || out[j] != "" {
				continue
			}
			for _, a := range at(answers, i) {
				if a == n[0] {
					out[j] = out[i] + n[0] + " › "
				}
			}
		}
	}
	return out
}
