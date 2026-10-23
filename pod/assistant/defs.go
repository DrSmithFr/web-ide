package assistant

import (
	"encoding/json"
	"fmt"
)

// Definitions of the tools offered to the model (OpenAI function format). The texts are read
// by the model: English, kept as they were in the page.

type obj = map[string]any

func str(description string) obj     { return obj{"type": "string", "description": description} }
func integer(description string) obj { return obj{"type": "integer", "description": description} }
func boolean(description string) obj { return obj{"type": "boolean", "description": description} }
func strList(description string) obj {
	return obj{"type": "array", "items": obj{"type": "string"}, "description": description}
}
func enum(description string, values ...string) obj {
	o := obj{"type": "string", "enum": values}
	if description != "" {
		o["description"] = description
	}
	return o
}

// Def is a tool definition, marshalled once.
type Def struct {
	Name string
	JSON json.RawMessage
}

// Func is the definition of a tool declared outside this package (a plugin's).
func Func(name, description string, props map[string]any, required ...string) Def {
	return fn(name, description, props, required...)
}

func fn(name, description string, props obj, required ...string) Def {
	if required == nil {
		required = []string{}
	}
	data, _ := json.Marshal(obj{"type": "function", "function": obj{"name": name, "description": description, "parameters": obj{"type": "object", "properties": props, "required": required}}})
	return Def{Name: name, JSON: data}
}

// MaxQuestions asked by one ask_user call; MaxDescription and MaxNote are the limits of the
// kanban (characters).
const (
	MaxQuestions   = 10
	MaxDescription = 1500
	MaxNote        = 1000
)

// AskUserDef is the ask_user tool, for the assistant (the MCP has its own, AskUserQuestions).
var AskUserDef = fn("ask_user",
	fmt.Sprintf("Asks the user one or more questions (1 to %d) when information is missing or a choice is theirs. Each question has a type that picks its look: choice (the default), idea, compare, rank or scenario. The user can also answer freely, say \"I don't know\" or \"Up to you\", and add a note to any question. Questions can form a small graph: a question with an id is asked only when an option with its id as next is chosen; the others are asked in order. After this call the turn stops until the answers come back (as the tool result). Write the questions in the user's language.", MaxQuestions),
	obj{"questions": obj{
		"type":        "array",
		"description": "The questions, asked one at a time",
		"items": obj{
			"type": "object",
			"properties": obj{
				"question":  str("The full question, ending with a question mark (for idea: the proposal)"),
				"id":        str("Short id, for a question asked only after an option leading to it (next)"),
				"header":    str(`Very short label (12 characters max), e.g. "Format"`),
				"type":      enum("choice: 2 to 6 options; idea: one proposal to validate, no options; compare: two approaches, exactly 2 options; rank: 2 to 8 options to order; scenario: a situation, then 2 to 6 options", QuestionTypes...),
				"situation": str("scenario: the concrete situation (a few sentences)"),
				"top":       integer("rank: only the top N is picked (N below the number of options)"),
				"options": obj{
					"type":        "array",
					"description": `The choices (none for idea); put the recommended option first with "(recommended)"`,
					"items": obj{"type": "object", "properties": obj{
						"label":       str("Choice (1 to 5 words)"),
						"description": str("What this choice implies"),
						"pros":        strList("What goes for it (short points)"),
						"cons":        strList("What goes against it (short points)"),
						"next":        str("id of the question asked next when this option is chosen"),
					}, "required": []string{"label"}},
				},
				"multiple": boolean("choice: several choices allowed"),
				"nextYes":  str("idea: id of the question asked after Yes or Exactly"),
				"nextNo":   str("idea: id of the question asked after No"),
			},
			"required": []string{"question"},
		},
	}}, "questions")

// AskUserQuestions is the schema of the questions of ask_user, for the MCP tool of the same
// name (the CLI agents ask through the Questions tool of the IDE).
func AskUserQuestions() map[string]any {
	var d struct {
		Function struct {
			Parameters struct {
				Properties map[string]map[string]any `json:"properties"`
			} `json:"parameters"`
		} `json:"function"`
	}
	_ = json.Unmarshal(AskUserDef.JSON, &d)
	return d.Function.Parameters.Properties["questions"]
}

var Complexities = []string{"low", "medium", "high"}

// MaxStory is the longest user story of a milestone, in characters.
const MaxStory = 500

// MilestoneDescription and MilestoneProps: the kanban_milestone tool, for the assistant and the
// MCP endpoint.
const MilestoneDescription = `Milestones group the tickets of one feature (across lineages) with its spec, user stories and feedback; the user validates them. Actions: list; get (id); create (title, description, spec); update (id; title, description, spec, objective); story_add (id, text: "As a <user>, I <can do>…"), story_edit (id, story, text), story_delete (id, story); feedback_add (id, kind, text, complexity), feedback_done / feedback_reopen (id, feedback); note (id, text: a decision). Only the user validates a story or a milestone. Put a ticket in a milestone with the milestone field of kanban_create / kanban_update.`

var MilestoneProps = obj{
	"action":      enum("What to do", "list", "get", "create", "update", "story_add", "story_edit", "story_delete", "feedback_add", "feedback_done", "feedback_reopen", "note"),
	"id":          integer("Milestone number (all actions but list and create)"),
	"title":       str("Title (create, update)"),
	"description": str(fmt.Sprintf("Description in Markdown, %d characters max: the feature, its scope (create, update)", MaxDescription)),
	"spec":        str("The spec: a path of the repository or an address (create, update)"),
	"story":       integer("User story id (story_edit, story_delete)"),
	"text":        str(fmt.Sprintf("User story (%d characters max), feedback or note (%d characters max)", MaxStory, MaxNote)),
	"kind":        enum("Kind of feedback (feedback_add)", "bug", "feature", "info"),
	"complexity":  enum("How hard handling the feedback is (feedback_add)", Complexities...),
	"feedback":    integer("Feedback id (feedback_done, feedback_reopen)"),
	"objective":   integer("Default objective of the milestone; its open tickets go into it (update; 0: none); see kanban_objective"),
}

// ObjectiveDescription and ObjectiveProps: the kanban_objective tool, for the assistant and the
// MCP endpoint.
const ObjectiveDescription = `Objectives are named deadlines (MVP, a launch…) in order: a milestone says what (one feature), an objective says when, taking tickets of several milestones and lone ones. A ticket has one objective at most, apart from its milestone. Actions: list (in order); get (id: its tickets grouped by milestone); create (title, description, due); update (id; title, description, due); note (id, text: a decision). Only the user reaches, abandons or reorders an objective. Put a ticket in an objective with the objective field of kanban_create / kanban_update, a milestone's open tickets with the objective field of kanban_milestone update.`

var ObjectiveProps = obj{
	"action":      enum("What to do", "list", "get", "create", "update", "note"),
	"id":          integer("Objective number (get, update, note)"),
	"title":       str("Title (create, update)"),
	"description": str(fmt.Sprintf("Description in Markdown, %d characters max: what reaching it means (create, update)", MaxDescription)),
	"due":         str("Date aimed at, YYYY-MM-DD; empty: none (create, update)"),
	"text":        str(fmt.Sprintf("Note, %d characters max (note)", MaxNote)),
}
