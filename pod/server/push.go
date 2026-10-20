package server

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/DrSmithFr/web-ide/lib/i18n"
	"github.com/DrSmithFr/web-ide/lib/identity"
	"github.com/DrSmithFr/web-ide/pod/kanban"
	"github.com/DrSmithFr/web-ide/pod/push"
)

// Push notifications (Web Push through the relay). The pod decides when to notify, since the
// page may be closed, and encrypts the payload for the subscription of each browser (package
// push): the relay forwards ciphertext only. A device that has a visible window on this pod is
// not notified; the page shows its own toasts there.

// pushTTL is how long a push service keeps a notification for a browser that is offline.
const pushTTL = 24 * 3600

// pushNote is one notification: its kind (the switch of the devices), the path of the app it
// opens, its tag (a newer one with the same tag replaces it) and its texts in a language.
type pushNote struct {
	kind, path, tag, urgency string
	text                     func(lang string) (title, body string)
}

func (s *Server) registerPush() {
	type devArg struct {
		Device string `json:"device"`
	}
	s.handle("push.status", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[devArg](p)
		if err != nil {
			return nil, err
		}
		out := map[string]any{"available": s.Push != nil && s.PushSend != nil, "kinds": push.Kinds}
		if s.Push != nil {
			if d, ok := s.Push.Get(a.Device); ok {
				out["subscribed"] = true
				out["choices"] = d.Kinds
				out["sub"] = d.Sub // the id at the relay, to drop it there
			}
		}
		return out, nil
	})
	s.handle("push.subscribe", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			Device, Sub, P256dh, Auth string
		}](p)
		if err != nil {
			return nil, err
		}
		if s.Push == nil || s.PushSend == nil {
			return nil, i18n.New("this pod has no relay")
		}
		if a.Device == "" || a.Sub == "" || a.P256dh == "" || a.Auth == "" {
			return nil, i18n.New("incomplete push subscription")
		}
		return nil, s.Push.Subscribe(a.Device, push.Device{Sub: a.Sub, P256dh: a.P256dh, Auth: a.Auth, Lang: c.language()})
	})
	s.handle("push.unsubscribe", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[devArg](p)
		if err != nil || s.Push == nil {
			return nil, err
		}
		return nil, s.Push.Unsubscribe(a.Device)
	})
	s.handle("push.settings", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			Device, Kind string
			On           bool
		}](p)
		if err != nil || s.Push == nil {
			return nil, err
		}
		return nil, s.Push.SetKind(a.Device, a.Kind, a.On)
	})
	// push.test sends a notification to this device, visible or not.
	s.handle("push.test", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[devArg](p)
		if err != nil {
			return nil, err
		}
		d, ok := push.Device{}, false
		if s.Push != nil {
			d, ok = s.Push.Get(a.Device)
		}
		if !ok || s.PushSend == nil {
			return nil, i18n.New("notifications are not enabled on this device")
		}
		return nil, s.sendPush(d, pushNote{kind: "test", path: "/", tag: "test", urgency: "normal", text: func(lang string) (string, string) {
			return i18n.T(lang, "Notifications work"), i18n.T(lang, "A test sent by %s", podName())
		}})
	})
	// window.visible: the page tells whether it is shown, and the browser device it runs on.
	s.handle("window.visible", func(ctx context.Context, c *Client, p json.RawMessage) (any, error) {
		a, err := bind[struct {
			Device  string
			Visible bool
		}](p)
		if err != nil {
			return nil, err
		}
		c.pushDevice.Store(a.Device)
		c.visible.Store(a.Visible)
		return nil, nil
	})
}

// podName names this pod in the test notification.
func podName() string {
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return "Web IDE"
}

// deviceVisible reports whether a device has a visible window on this pod.
func (s *Server) deviceVisible(device string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.clients {
		if d, _ := c.pushDevice.Load().(string); d == device && c.visible.Load() {
			return true
		}
	}
	return false
}

// notify sends a notification to the devices that want its kind and show no window.
func (s *Server) notify(n pushNote) {
	if s.Push == nil || s.PushSend == nil {
		return
	}
	for dev, d := range s.Push.For(n.kind) {
		if !s.deviceVisible(dev) {
			_ = s.sendPush(d, n)
		}
	}
}

// sendPush encrypts a notification for one device and gives it to the relay.
func (s *Server) sendPush(d push.Device, n pushNote) error {
	lang := d.Lang
	if lang == "" {
		lang = "en"
	}
	title, body := n.text(lang)
	payload, _ := json.Marshal(map[string]string{"title": clip(title, 120), "body": clip(body, 400), "url": s.pushLink(n.path), "tag": n.tag})
	key, err := identity.B64d(d.P256dh)
	if err != nil {
		return err
	}
	auth, err := identity.B64d(d.Auth)
	if err != nil {
		return err
	}
	ct, err := push.Encrypt(key, auth, payload)
	if err != nil {
		return err
	}
	return s.PushSend(d.Sub, ct, pushTTL, n.urgency, pushTopic(n.tag))
}

// pushLink is the bootstrap link of the relay that opens a path of the app on this pod.
func (s *Server) pushLink(path string) string {
	pod := ""
	if s.RelayStatus != nil {
		pod = s.RelayStatus().PodID
	}
	return "/#pod=" + url.QueryEscape(pod) + "&open=" + url.QueryEscape(path)
}

// pushTopic is the Topic header of a tag (32 characters of the base64url alphabet at most): a
// newer notification of the same conversation replaces the one still queued.
func pushTopic(tag string) string {
	var b strings.Builder
	for _, r := range tag {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	t := b.String()
	if len(t) > 32 {
		t = t[len(t)-32:]
	}
	return t
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// projectName is the name of a project for a notification (its id when unknown).
func (s *Server) projectName(id string) string {
	if p, ok := s.Projects.Get(id); ok {
		return p.Name()
	}
	return id
}

// notifyAssistant: a turn of a root conversation ended (asks: it waits for the user).
func (s *Server) notifyAssistant(asks bool, root, chatID, title string) {
	kind, head, urgency := push.Finished, "The assistant finished", "normal"
	if asks {
		kind, head, urgency = push.Asks, "The assistant needs you", "high"
	}
	name := s.projectName(root)
	go s.notify(pushNote{kind: kind, path: "/project/" + url.PathEscape(root) + "?chat=" + url.QueryEscape(chatID), tag: chatID, urgency: urgency,
		text: func(lang string) (string, string) { return i18n.T(lang, head), name + " · " + title }})
}

// notifyTests: the task of a saved command flagged test ended with a code that is not 0.
func (s *Server) notifyTests(projectID, consoleID string, code int) {
	s.mu.Lock()
	rt := s.runtimes[projectID]
	s.mu.Unlock()
	if rt == nil || code == 0 || s.Push == nil {
		return
	}
	commandID := ""
	for _, i := range rt.Consoles.List() {
		if i.ID == consoleID {
			commandID = i.CommandID
		}
	}
	if commandID == "" {
		return
	}
	cmd, err := s.findCommand(rt, commandID)
	if err != nil || cmd.Flag != "test" {
		return
	}
	name := s.projectName(projectID)
	s.notify(pushNote{kind: push.Tests, path: "/project/" + url.PathEscape(projectID), tag: "tests-" + cmd.ID, urgency: "normal",
		text: func(lang string) (string, string) {
			return i18n.T(lang, "Tests failed"), i18n.T(lang, "%s · %s ended with code %d", name, cmd.Name, code)
		}})
}

// notifyFeedback: the assistant or Claude Code added feedback to a ticket.
func (s *Server) notifyFeedback(loc kanban.Location, id int64, text string) {
	if s.Push == nil {
		return
	}
	ticket := ""
	if t, err := s.Kanban.Get(loc, id); err == nil {
		ticket = t.Title
	}
	name := s.projectName(loc.Project)
	n := strconv.FormatInt(id, 10)
	go s.notify(pushNote{kind: push.Feedback, path: "/project/" + url.PathEscape(loc.Project) + "?ticket=" + n, tag: "ticket-" + n, urgency: "normal",
		text: func(lang string) (string, string) {
			return i18n.T(lang, "Feedback on #%d %s", id, ticket), name + " · " + text
		}})
}
