// Package i18n translates the messages the pod sends to the page. Messages are written in
// English (the format string is the key); a catalog gives their text in another language.
// Each window tells the pod its language, and errors are translated when they are sent.
package i18n

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Languages is the one list of the languages of the pod, English (the source text, no
// catalog) first. Each other one has a catalog `<code>.json` in this folder; the page keeps
// its own list in web/src/i18n/languages.json.
var Languages = []string{"en", "fr", "es"}

//go:embed *.json
var files embed.FS

var catalogs = map[string]map[string]string{}

func init() {
	for _, lang := range Languages[1:] {
		data, err := files.ReadFile(lang + ".json")
		if err != nil {
			panic("i18n: " + err.Error())
		}
		c := map[string]string{}
		if err := json.Unmarshal(data, &c); err != nil {
			panic("i18n: " + lang + ".json: " + err.Error())
		}
		catalogs[lang] = c
	}
}

// Detect gives the language of an Accept-Language header: the first language of the header
// that is in the list, else English.
func Detect(acceptLanguage string) string {
	for _, part := range strings.Split(acceptLanguage, ",") {
		tag, _, _ := strings.Cut(part, ";")
		code, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(tag)), "-")
		if Supported(code) {
			return code
		}
	}
	return "en"
}

// Supported tells whether a language has a catalog (English needs none).
func Supported(lang string) bool {
	for _, l := range Languages {
		if l == lang {
			return true
		}
	}
	return false
}

// T formats a message in a language.
func T(lang, format string, args ...any) string {
	if tr, ok := catalogs[lang][format]; ok {
		format = tr
	}
	format = strings.ReplaceAll(format, "%w", "%v")
	for i, a := range args {
		switch v := a.(type) {
		case error:
			args[i] = Translate(lang, v)
		case Text:
			args[i] = T(lang, string(v))
		}
	}
	return fmt.Sprintf(format, args...)
}

// Text is an argument of a message that is translated too (a status name…).
type Text string

// Error is a message that can be translated: its English text is Error().
type Error struct {
	format string
	args   []any
	err    error // the English error, wrapping the %w argument if any
}

func (e *Error) Error() string { return e.err.Error() }
func (e *Error) Unwrap() error { return errors.Unwrap(e.err) }

// Errorf is fmt.Errorf with a translatable message (%w wraps as usual).
func Errorf(format string, args ...any) error {
	return &Error{format: format, args: args, err: fmt.Errorf(format, args...)}
}

// New is errors.New with a translatable message.
func New(text string) error {
	return &Error{format: "%s", args: nil, err: errors.New(text)}
}

// Translate gives the message of an error in a language. Errors that were not created by
// this package (from git, a database, the network…) keep their own text.
func Translate(lang string, err error) string {
	var e *Error
	if !errors.As(err, &e) || e.Error() != err.Error() {
		return err.Error()
	}
	if e.args == nil && e.format == "%s" {
		if tr, ok := catalogs[lang][e.err.Error()]; ok {
			return tr
		}
		return e.err.Error()
	}
	args := append([]any(nil), e.args...)
	return T(lang, e.format, args...)
}
