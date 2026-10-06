package agent

import "encoding/json"

// PageRef is a page of the board of a conversation: a doodle or an image sent by the user
// (an attachment), or a page drawn by the model (Message.Page). Numbered from 1 in message
// order, then attachment order: web/src/llm/board/pages.ts counts the same way.
type PageRef struct {
	Number int
	Name   string
	Kind   string          // doodle | image | model
	Doc    json.RawMessage // nil when the image of an attachment is not kept
	PNG    string          // doodles and model pages (joined to the tickets)
}

type pageAttachment struct {
	Name   string          `json:"name"`
	Kind   string          `json:"kind"`
	Doodle json.RawMessage `json:"doodle"`
	PNG    string          `json:"png"`
	W      int             `json:"w"`
	H      int             `json:"h"`
}

func Pages(msgs []*Message) []PageRef {
	var out []PageRef
	for _, m := range msgs {
		var atts []pageAttachment
		if len(m.Attachments) > 0 && json.Unmarshal(m.Attachments, &atts) == nil {
			images := imageSources(m, atts)
			k := 0
			for _, a := range atts {
				switch {
				case a.Kind == "doodle" && len(a.Doodle) > 0 && string(a.Doodle) != "null":
					out = append(out, PageRef{Number: len(out) + 1, Name: a.Name, Kind: "doodle", Doc: a.Doodle, PNG: a.PNG})
				case a.Kind == "image" && a.W > 0 && a.H > 0:
					p := PageRef{Number: len(out) + 1, Name: a.Name, Kind: "image"}
					if k < len(images) {
						p.Doc = imageDoc(images[k], a.W, a.H)
					}
					out = append(out, p)
				}
				if a.Kind == "image" {
					k++
				}
			}
		}
		if m.Page != nil {
			out = append(out, PageRef{Number: len(out) + 1, Name: m.Page.Name, Kind: "model", Doc: m.Page.Doc, PNG: m.Page.PNG})
		}
	}
	return out
}

// imageSources are the images of the image attachments of a message, in order: its image
// parts that are not the PNG of a doodle. None when video frames or PDF pages mix in.
func imageSources(m *Message, atts []pageAttachment) []string {
	doodles := map[string]bool{}
	for _, a := range atts {
		if a.Kind == "video" || a.Kind == "pdf" {
			return nil
		}
		if a.Kind == "doodle" {
			doodles[a.PNG] = true
		}
	}
	var parts []struct {
		Type     string `json:"type"`
		ImageURL struct {
			URL string `json:"url"`
		} `json:"image_url"`
	}
	_ = json.Unmarshal(m.Content, &parts)
	var out []string
	for _, p := range parts {
		if p.Type == "image_url" && !doodles[p.ImageURL.URL] {
			out = append(out, p.ImageURL.URL)
		}
	}
	return out
}

// imageDoc is the doodle document of an image: the image as background, nothing drawn.
func imageDoc(src string, w, h int) json.RawMessage {
	data, _ := json.Marshal(map[string]any{
		"v": 1, "preset": "image", "elements": []any{},
		"frame":      map[string]int{"x": 0, "y": 0, "w": w, "h": h},
		"background": map[string]any{"src": src, "x": 0, "y": 0, "w": w, "h": h},
	})
	return data
}
