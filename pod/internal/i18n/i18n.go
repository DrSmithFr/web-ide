// Package i18n translates the messages the pod sends to the page. Messages are written in
// English (the format string is the key); a catalog gives their text in another language.
// Each window tells the pod its language, and errors are translated when they are sent.
package i18n

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

//go:embed fr.json
var frJSON []byte

var catalogs = map[string]map[string]string{"fr": {}}

func init() {
	if err := json.Unmarshal(frJSON, ptr(catalogs["fr"])); err != nil {
		panic("i18n: fr.json: " + err.Error())
	}
}

func ptr[T any](v T) *T { return &v }

// Supported tells whether a language has a catalog (English needs none).
func Supported(lang string) bool {
	_, ok := catalogs[lang]
	return ok || lang == "en"
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
