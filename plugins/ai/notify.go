package ai

import (
	"net/url"

	"antennae.space/ide/lib/i18n"
	"antennae.space/ide/pod/plugins"
	"antennae.space/ide/pod/push"
)

// notifyChat sends the push notification of a turn of a root conversation that ended (asks:
// it waits for the user).
func (s *A) notifyChat(asks bool, root, chatID, title string) {
	kind, head, urgency := push.Finished, "The assistant finished", "normal"
	if asks {
		kind, head, urgency = push.Asks, "The assistant needs you", "high"
	}
	name, _, _ := s.pod.Project(root)
	if name == "" {
		name = root
	}
	s.pod.Notify(plugins.Note{Kind: kind, Path: "/project/" + url.PathEscape(root) + "?chat=" + url.QueryEscape(chatID), Tag: chatID, Urgency: urgency,
		Text: func(lang string) (string, string) { return i18n.T(lang, head), name + " · " + title }})
}
