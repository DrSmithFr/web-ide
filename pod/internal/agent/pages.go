package agent

import "encoding/json"

// PageRef is a page of the board of a conversation: a doodle sent by the user (an attachment
// with its document) or a page drawn by the model (Message.Page). Numbered from 1 in message
// order, then attachment order: web/src/llm/board/pages.ts counts the same way.
type PageRef struct {
	Number int
	Name   string
	Doc    json.RawMessage
	From   string // user | model
	PNG    string
}

func Pages(msgs []*Message) []PageRef {
	var out []PageRef
	for _, m := range msgs {
		var atts []struct {
			Name   string          `json:"name"`
			Kind   string          `json:"kind"`
			Doodle json.RawMessage `json:"doodle"`
			PNG    string          `json:"png"`
		}
		if len(m.Attachments) > 0 && json.Unmarshal(m.Attachments, &atts) == nil {
			for _, a := range atts {
				if a.Kind == "doodle" && len(a.Doodle) > 0 && string(a.Doodle) != "null" {
					out = append(out, PageRef{Number: len(out) + 1, Name: a.Name, Doc: a.Doodle, From: "user", PNG: a.PNG})
				}
			}
		}
		if m.Page != nil {
			out = append(out, PageRef{Number: len(out) + 1, Name: m.Page.Name, Doc: m.Page.Doc, From: "model", PNG: m.Page.PNG})
		}
	}
	return out
}
