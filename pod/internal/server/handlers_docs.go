package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"path"
	"strings"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
)

// registerDocs serves the documentation embedded in the binary (pod/docsdist), read only and
// not tied to a project: the Help of the window. Paths are those of the repo ("docs/guide.md").
func (s *Server) registerDocs() {
	s.handle("docs.read", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct{ Path string }](p)
		if err != nil {
			return nil, err
		}
		name, err := s.docPath(a.Path)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(path.Ext(name), ".md") {
			return nil, i18n.New("not a documentation page")
		}
		b, err := fs.ReadFile(s.Docs, name)
		if err != nil {
			return nil, i18n.New("no such documentation page")
		}
		return map[string]string{"text": string(b)}, nil
	})
	s.handle("docs.image", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct{ Path string }](p)
		if err != nil {
			return nil, err
		}
		name, err := s.docPath(a.Path)
		if err != nil {
			return nil, err
		}
		mime, ok := imageTypes[strings.ToLower(path.Ext(name))]
		if !ok {
			return nil, i18n.New("not an image")
		}
		b, err := fs.ReadFile(s.Docs, name)
		if err != nil {
			return nil, i18n.New("no such documentation page")
		}
		if len(b) > maxImage {
			return nil, i18n.New("image too large")
		}
		return map[string]string{"url": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b)}, nil
	})
}

// docPath cleans a path of the documentation: relative to its root, never outside it.
func (s *Server) docPath(p string) (string, error) {
	if s.Docs == nil {
		return "", i18n.New("no such documentation page")
	}
	name := path.Clean(strings.TrimPrefix(p, "/"))
	if name == "." || name == ".." || strings.HasPrefix(name, "../") {
		return "", i18n.New("no such documentation page")
	}
	return name, nil
}
