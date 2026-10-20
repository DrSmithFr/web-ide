package config

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"regexp"
	"strings"

	"github.com/DrSmithFr/web-ide/pod/store"
)

// RelayIDs is the identity of the pod with its relay: an id and a secret, generated on
// first use and kept in relay.json (0600). The relay uses them to recognize the pod; the
// pod token never goes to it (it is proved inside the tunnel).
type RelayIDs struct {
	PodID  string `json:"podId"`
	Secret string `json:"secret"`
}

// Relay returns the relay ids of the pod, creating them on first start.
func Relay(st *store.Store) (*RelayIDs, error) {
	var ids RelayIDs
	if err := st.ReadJSON("relay.json", &ids); err == nil && ids.PodID != "" && ids.Secret != "" {
		return &ids, nil
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	ids = RelayIDs{PodID: podName(), Secret: hex.EncodeToString(buf)}
	if err := st.WriteJSON("relay.json", &ids); err != nil {
		return nil, err
	}
	return &ids, nil
}

// podName is a readable pod id: the hostname, or a random name when the host has none.
func podName() string {
	h, err := os.Hostname()
	if err != nil {
		h = ""
	}
	h = strings.ToLower(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(h, "-"))
	h = strings.Trim(h, "-")
	if h == "" {
		buf := make([]byte, 4)
		_, _ = rand.Read(buf)
		return "pod-" + hex.EncodeToString(buf)
	}
	return h
}
