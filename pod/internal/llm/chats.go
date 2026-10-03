package llm

import (
	"encoding/json"
	"errors"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
)

// Conversations are saved per project in chats/<project>/<id>.json. The page owns their
// format; the pod only reads the fields needed for the list.

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type ChatInfo struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Updated int64  `json:"updated"`
}

func chatFile(project, id string) (string, error) {
	if !idPattern.MatchString(project) || !idPattern.MatchString(id) {
		return "", errors.New("identifiant de conversation invalide")
	}
	return path.Join("chats", project, id+".json"), nil
}

func (m *Manager) ListChats(project string) ([]ChatInfo, error) {
	if !idPattern.MatchString(project) {
		return nil, errors.New("projet invalide")
	}
	entries, err := os.ReadDir(m.st.Path("chats", project))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	list := []ChatInfo{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		var info ChatInfo
		if m.st.ReadJSON(path.Join("chats", project, name), &info) == nil && info.ID != "" {
			list = append(list, info)
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Updated > list[j].Updated })
	return list, nil
}

func (m *Manager) GetChat(project, id string) (json.RawMessage, error) {
	name, err := chatFile(project, id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(m.st.Path(name))
	if os.IsNotExist(err) {
		return nil, errors.New("conversation introuvable")
	}
	return data, err
}

func (m *Manager) SaveChat(project string, chat json.RawMessage) error {
	var info ChatInfo
	if err := json.Unmarshal(chat, &info); err != nil {
		return err
	}
	name, err := chatFile(project, info.ID)
	if err != nil {
		return err
	}
	return m.st.WriteFile(name, chat)
}

func (m *Manager) DeleteChat(project, id string) error {
	name, err := chatFile(project, id)
	if err != nil {
		return err
	}
	return m.st.Remove(name)
}
