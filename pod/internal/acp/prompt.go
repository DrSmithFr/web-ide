package acp

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// PromptCaps are the content blocks the agent takes besides text and resource links.
type PromptCaps struct {
	Image           bool `json:"image"`
	EmbeddedContext bool `json:"embeddedContext"`
}

// Part is a piece of a prompt besides its text, as the page sends it: an image (base64 data)
// or a text (a file read by the page, the description of a doodle).
type Part struct {
	Kind string `json:"kind"` // image | text
	Name string `json:"name"`
	Mime string `json:"mime,omitempty"`
	Data string `json:"data,omitempty"`
	Text string `json:"text,omitempty"`
}

// Attachment is a part of a prompt as the thread keeps it: an image is a file next to the
// session (File), served by agents.image; a text keeps its name only.
type Attachment struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	File string `json:"file,omitempty"`
	Mime string `json:"mime,omitempty"`
}

// queued is a prompt waiting for the end of the turn; its images are already files.
type queued struct {
	Text  string       `json:"text"`
	Atts  []Attachment `json:"atts,omitempty"`
	Texts []string     `json:"texts,omitempty"`
}

// QueuedInfo is a queued prompt as the page shows it.
type QueuedInfo struct {
	Text        string `json:"text"`
	Attachments int    `json:"attachments,omitempty"`
}

func (s *Session) imageDir() string { return filepath.Join(s.m.Dir, s.info.ID) }

var mimeExt = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/webp": ".webp", "image/gif": ".gif"}

// keep turns the parts of the page into a queued prompt: the images written to files.
func (s *Session) keep(text string, parts []Part) (queued, error) {
	q := queued{Text: text}
	for _, p := range parts {
		switch p.Kind {
		case "image":
			if !s.caps.Image {
				return q, i18n.New("this agent does not take images")
			}
			ext, ok := mimeExt[p.Mime]
			if !ok {
				return q, i18n.Errorf("unsupported image type: %s", p.Mime)
			}
			data, err := base64.StdEncoding.DecodeString(p.Data)
			if err != nil {
				return q, err
			}
			if err := os.MkdirAll(s.imageDir(), 0o700); err != nil {
				return q, err
			}
			s.images++
			name := fmt.Sprintf("img-%d%s", s.images, ext)
			if err := os.WriteFile(filepath.Join(s.imageDir(), name), data, 0o600); err != nil {
				return q, err
			}
			q.Atts = append(q.Atts, Attachment{Name: p.Name, Kind: "image", File: name, Mime: p.Mime})
		case "text":
			q.Atts = append(q.Atts, Attachment{Name: p.Name, Kind: "text"})
			q.Texts = append(q.Texts, p.Text)
		}
	}
	return q, nil
}

var mention = regexp.MustCompile(`(^|\s)@([^\s@]+)`)

// blocks builds the ACP content of a prompt: its text, a resource link per @path of the text
// that exists, its images, the texts of its files.
func (s *Session) blocks(q queued) ([]map[string]any, error) {
	out := []map[string]any{{"type": "text", "text": q.Text}}
	seen := map[string]bool{}
	for _, m := range mention.FindAllStringSubmatch(q.Text, -1) {
		rel := strings.TrimRight(m[2], ".,;:!?)")
		p := rel
		if !filepath.IsAbs(p) {
			p = filepath.Join(s.info.Cwd, rel)
		}
		if seen[p] {
			continue
		}
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			seen[p] = true
			out = append(out, map[string]any{"type": "resource_link", "uri": "file://" + p, "name": filepath.Base(p)})
		}
	}
	for _, a := range q.Atts {
		if a.Kind != "image" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.imageDir(), a.File))
		if err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"type": "image", "mimeType": a.Mime, "data": base64.StdEncoding.EncodeToString(data)})
	}
	for _, t := range q.Texts {
		out = append(out, map[string]any{"type": "text", "text": t})
	}
	return out, nil
}

// send starts a turn with q (under s.mu, the agent ready).
func (s *Session) send(q queued) error {
	content, err := s.blocks(q)
	if err != nil {
		return err
	}
	s.nextID++
	id := s.nextID
	if err := s.request(id, "session/prompt", map[string]any{"sessionId": s.acp, "prompt": content}); err != nil {
		return err
	}
	s.prompt = id
	s.beginTurn()
	s.th.plan = -1
	s.entry(s.th.add(Entry{Kind: "user", Text: q.Text, Attachments: q.Atts}))
	s.setState(Busy, "")
	return nil
}

// Prompt sends a prompt, or queues it while a turn runs (or the agent starts): it goes at
// the end of the turn.
func (s *Session) Prompt(text string, parts ...Part) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, err := s.keep(text, parts)
	if err != nil {
		return err
	}
	switch s.info.State {
	case Ready:
		return s.send(q)
	case Busy, Asking, Starting:
		s.queue = append(s.queue, q)
		s.queueChanged()
		return nil
	}
	return i18n.New("the agent is not ready")
}

// next sends the first queued prompt once the agent is ready (under s.mu).
func (s *Session) next() {
	if s.info.State != Ready || len(s.queue) == 0 {
		return
	}
	q := s.queue[0]
	s.queue = s.queue[1:]
	s.queueChanged()
	if err := s.send(q); err != nil {
		s.notice("error", err.Error())
	}
}

// Unqueue takes a queued prompt back; its text is returned to the page.
func (s *Session) Unqueue(index int) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if index < 0 || index >= len(s.queue) {
		return "", i18n.New("this prompt is no longer queued")
	}
	text := s.queue[index].Text
	s.queue = append(s.queue[:index:index], s.queue[index+1:]...)
	s.queueChanged()
	return text, nil
}

func (s *Session) queueChanged() {
	// A new slice: the info sent before keeps its own.
	s.info.Queue = nil
	for _, q := range s.queue {
		s.info.Queue = append(s.info.Queue, QueuedInfo{Text: q.Text, Attachments: len(q.Atts)})
	}
	s.changed()
}

// Image returns an image of the thread.
func (s *Session) Image(file string) ([]byte, string, error) {
	if file != filepath.Base(file) || !strings.HasPrefix(file, "img-") {
		return nil, "", i18n.New("image not found")
	}
	s.mu.Lock()
	dir := s.imageDir()
	s.mu.Unlock()
	data, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		return nil, "", i18n.New("image not found")
	}
	mime := "image/png"
	for m, ext := range mimeExt {
		if strings.HasSuffix(file, ext) {
			mime = m
		}
	}
	return data, mime, nil
}
