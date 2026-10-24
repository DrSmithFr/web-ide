package relay

import (
	"strings"

	"antennae.space/ide/lib/i18n"
	"antennae.space/ide/lib/identity"
)

// The sync messages, on the pod link and on the admin link. The relay stores encrypted blobs and
// the sync key wrapped for each device: it holds no sync key, so it cannot read a value, only
// check a signature against a device id it knows belongs to the account.
//
//   - a device publishes its X25519 key, signed by its device key;
//   - an admin (a device holding the account key) wraps the key of the current epoch for each
//     device that published one, and posts the wrap;
//   - a pod reads and writes the blob of a kind, with compare-and-swap on seq;
//   - a stored write is pushed to the online pods of the account.

// syncReply answers a write: kept says whether it changed the stored state.
func (s *Server) syncReply(kept bool) string {
	if kept {
		return "stored"
	}
	return "older"
}

// handleSync runs one sync message on an admin link.
func (s *Server) handleSync(c *identityConn, m idMsg) {
	switch m.Type {
	case "syncKey":
		if err := s.store.StoreSyncKey(m.SyncKey); err != nil {
			_ = c.send(idMsg{Type: "error", Message: i18n.Translate(c.lang, err)})
			return
		}
		s.mu.Lock()
		c.device = m.SyncKey.Device
		s.mu.Unlock()
		_ = c.send(idMsg{Type: "syncKey", Account: c.account, SyncKey: m.SyncKey, Message: "stored"})
		s.pushSyncKey(c.account, m.SyncKey, c)
	case "syncKeys":
		list, err := s.store.SyncKeys(c.account)
		if err != nil {
			_ = c.send(idMsg{Type: "error", Message: i18n.Translate(c.lang, err)})
			return
		}
		_ = c.send(idMsg{Type: "syncKeys", Account: c.account, SyncKeys: list})
	case "wrap":
		if err := s.store.PutWrap(m.Wrap); err != nil {
			_ = c.send(idMsg{Type: "error", Message: i18n.Translate(c.lang, err)})
			return
		}
		_ = c.send(idMsg{Type: "wrap", Account: c.account, Wrap: m.Wrap, Message: "stored"})
		s.pushWrap(c.account, m.Wrap, c)
	case "wraps":
		list, err := s.store.Wraps(c.account)
		if err != nil {
			_ = c.send(idMsg{Type: "error", Message: i18n.Translate(c.lang, err)})
			return
		}
		_ = c.send(idMsg{Type: "wraps", Account: c.account, Wraps: list})
	case "wrapGet":
		w, updated, err := s.store.Wrap(c.account, m.Device)
		if err != nil {
			_ = c.send(idMsg{Type: "error", Message: i18n.Translate(c.lang, err)})
			return
		}
		_ = c.send(idMsg{Type: "wrap", Account: c.account, Device: m.Device, Wrap: w, Updated: updated})
	case "blobGet":
		if isKindPrefix(m.Kind) {
			s.sendBlobs(c, nil, c.account, m.Kind)
			return
		}
		b, updated, err := s.store.Blob(c.account, m.Kind)
		if err != nil {
			_ = c.send(idMsg{Type: "error", Message: i18n.Translate(c.lang, err)})
			return
		}
		_ = c.send(idMsg{Type: "blob", Account: c.account, Kind: m.Kind, Blob: b, Updated: updated})
	case "blobPut":
		kept, err := s.store.PutBlob(m.Blob)
		if err != nil {
			_ = c.send(idMsg{Type: "error", Message: i18n.Translate(c.lang, err)})
			return
		}
		_ = c.send(idMsg{Type: "blob", Account: c.account, Kind: m.Kind, Blob: m.Blob, Message: s.syncReply(kept)})
		if kept {
			s.pushBlob(c.account, m.Blob, c)
		}
	}
}

// handlePodSync runs one sync message on a pod link. The pod is not a window, so its errors keep
// their English text, as on the rest of its link.
func (s *Server) handlePodSync(pc *podConn, m sigMsg) {
	switch m.Type {
	case "syncKey":
		if err := s.store.StoreSyncKey(m.SyncKey); err != nil {
			pc.send(sigMsg{Type: "error", Message: err.Error()})
			return
		}
		s.mu.Lock()
		pc.device = m.SyncKey.Device
		s.mu.Unlock()
		pc.send(sigMsg{Type: "syncKey", Account: pc.account, SyncKey: m.SyncKey, Message: "stored"})
		s.pushSyncKey(pc.account, m.SyncKey, nil)
	case "wrapGet":
		w, updated, err := s.store.Wrap(pc.account, m.Device)
		if err != nil {
			pc.send(sigMsg{Type: "error", Message: err.Error()})
			return
		}
		pc.send(sigMsg{Type: "wrap", Account: pc.account, Device: m.Device, Wrap: w, Updated: updated})
	case "blobGet":
		if isKindPrefix(m.Kind) {
			s.sendBlobs(nil, pc, pc.account, m.Kind)
			return
		}
		b, updated, err := s.store.Blob(pc.account, m.Kind)
		if err != nil {
			pc.send(sigMsg{Type: "error", Message: err.Error()})
			return
		}
		pc.send(sigMsg{Type: "blob", Account: pc.account, Kind: m.Kind, Blob: b, Updated: updated})
	case "blobPut":
		kept, err := s.store.PutBlob(m.Blob)
		if err != nil {
			pc.send(sigMsg{Type: "error", Message: err.Error()})
			return
		}
		pc.send(sigMsg{Type: "blob", Account: pc.account, Kind: m.Kind, Blob: m.Blob, Message: s.syncReply(kept)})
		if kept {
			s.pushBlob(pc.account, m.Blob, nil)
		}
	}
}

// isKindPrefix reports whether a blobGet asks for a family of kinds ("projects:") rather than one
// kind: the project lists of every pod of the account.
func isKindPrefix(kind string) bool { return strings.HasSuffix(kind, ":") }

// sendBlobs answers a request for a family of kinds, one blob message per stored write. The relay
// still only passes ciphertext.
func (s *Server) sendBlobs(c *identityConn, pc *podConn, account, prefix string) {
	recs, updated, err := s.store.Blobs(account, prefix)
	if err != nil {
		if c != nil {
			_ = c.send(idMsg{Type: "error", Message: i18n.Translate(c.lang, err)})
		}
		if pc != nil {
			pc.send(sigMsg{Type: "error", Message: err.Error()})
		}
		return
	}
	for _, r := range recs {
		if c != nil {
			_ = c.send(idMsg{Type: "blob", Account: account, Kind: r.Kind, Blob: r, Updated: updated[r.Kind]})
		}
		if pc != nil {
			pc.send(sigMsg{Type: "blob", Account: account, Kind: r.Kind, Blob: r, Updated: updated[r.Kind]})
		}
	}
}

// The sync pushes go to the links and pods of an account, except the one that posted the message
// (it already got the answer). `from` is the link or the pod that wrote it.

// pushSyncKey tells the admin links of an account that a device published its X25519 key: the
// Members panel wraps the epoch key for it.
func (s *Server) pushSyncKey(account string, rec *identity.SyncKeyRecord, from any) {
	s.mu.Lock()
	var links []*identityConn
	for c := range s.links {
		if c.account == account && c != from {
			links = append(links, c)
		}
	}
	s.mu.Unlock()
	for _, c := range links {
		_ = c.send(idMsg{Type: "syncKey", Account: account, SyncKey: rec})
	}
}

// pushWrap tells the device a wrap was made for it (the pod reads it and gets the epoch key). A
// wrap is only read by the device it names, so it is sent to that link or pod; a link that never
// published a sync key is an admin panel, which shows the wraps of the whole account. It also goes
// to every pod of the account: a pod with no link to the relay gets its wrap through a peer of its
// cluster, which passes it on over the LAN. The wrap is sealed for its device, so the others learn
// nothing from it.
func (s *Server) pushWrap(account string, w *identity.WrapRecord, from any) {
	s.mu.Lock()
	var links []*identityConn
	for c := range s.links {
		if c.account == account && c != from && (c.device == "" || c.device == w.Device) {
			links = append(links, c)
		}
	}
	var pods []*podConn
	for _, p := range s.pods {
		if p.account == account && p != from {
			pods = append(pods, p)
		}
	}
	s.mu.Unlock()
	for _, c := range links {
		_ = c.send(idMsg{Type: "wrap", Account: account, Wrap: w})
	}
	for _, p := range pods {
		_ = p.send(sigMsg{Type: "wrap", Account: account, Device: w.Device, Wrap: w})
	}
}

// pushBlob sends a stored write to the online links and pods of the account. The relay cannot read
// the blob: it only passes the ciphertext through.
func (s *Server) pushBlob(account string, rec *identity.BlobRecord, from any) {
	s.mu.Lock()
	var links []*identityConn
	for c := range s.links {
		if c.account == account && c != from {
			links = append(links, c)
		}
	}
	var pods []*podConn
	for _, p := range s.pods {
		if p.account == account && p != from {
			pods = append(pods, p)
		}
	}
	s.mu.Unlock()
	for _, c := range links {
		_ = c.send(idMsg{Type: "blob", Account: account, Kind: rec.Kind, Blob: rec})
	}
	for _, p := range pods {
		_ = p.send(sigMsg{Type: "blob", Account: account, Kind: rec.Kind, Blob: rec})
	}
}
