package identity

import _ "embed"

//go:embed web/identity.js
var moduleJS []byte

// ModuleJS is the identity module, the one file the bootstrap page and the app import: the
// browser and the pod must agree on every byte a signature covers, so there is a single copy,
// served under /_relay/identity.js by the relay and by the pod (127.0.0.1).
func ModuleJS() []byte { return moduleJS }

// WordlistBytes is the BIP39 English list, served to the browser under /_relay/bip39.txt: a
// phrase accepted on one side is accepted on the other.
func WordlistBytes() []byte { return []byte(wordlistRaw) }
