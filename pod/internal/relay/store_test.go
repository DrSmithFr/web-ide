package relay

import (
	"path/filepath"
	"testing"
)

func TestStoreRegisterAndSecret(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.Register("pod-a", "secret-1", "laptop"); err != nil {
		t.Fatal("first registration:", err)
	}
	if err := st.Register("pod-a", "secret-1", ""); err != nil {
		t.Fatal("re-register with the same secret must pass:", err)
	}
	if err := st.Register("pod-a", "other", ""); err == nil {
		t.Fatal("a re-register with a different secret must fail")
	}
	if !st.OnlinePod("pod-a") {
		t.Fatal("pod-a must be online after registration")
	}
	st.Offline("pod-a")
	if st.OnlinePod("pod-a") {
		t.Fatal("pod-a must be offline after Offline")
	}
	if st.Name("pod-a") != "laptop" {
		t.Fatal("the name must be stored")
	}
}

func TestStoreNewPodNeedsSecret(t *testing.T) {
	st, _ := OpenStore(filepath.Join(t.TempDir(), "relay.db"))
	defer st.Close()
	if err := st.Register("ghost", "", ""); err == nil {
		t.Fatal("a new pod without a secret must be refused")
	}
	if st.OnlinePod("ghost") {
		t.Fatal("a refused pod must not be online")
	}
}
