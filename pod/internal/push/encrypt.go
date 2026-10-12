// Package push holds the Web Push side of the pod: the encryption of a payload for the
// subscription of a browser (RFC 8291, aes128gcm of RFC 8188) and the subscriptions of the
// browsers with their choices (push.json). The relay only forwards the ciphertext: it signs
// the request to the push service (VAPID) and never sees a title or a project name.
package push

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

// recordSize is the record size of the header (one record: the payload is under 4 KB).
const recordSize = 4096

// Encrypt encrypts a payload for a subscription: p256dh is the public key of the browser (65
// bytes, uncompressed P-256), auth its 16-byte secret. A fresh key pair and salt are made.
func Encrypt(p256dh, auth, plaintext []byte) ([]byte, error) {
	as, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	return encrypt(as, salt, p256dh, auth, plaintext)
}

// encrypt is Encrypt with the key pair of the application server and the salt given (the RFC
// vector).
func encrypt(as *ecdh.PrivateKey, salt, p256dh, auth, plaintext []byte) ([]byte, error) {
	if len(auth) != 16 || len(salt) != 16 {
		return nil, errors.New("push: bad auth secret or salt")
	}
	if len(plaintext) > recordSize-16-1-86 {
		return nil, errors.New("push: payload too long")
	}
	ua, err := ecdh.P256().NewPublicKey(p256dh)
	if err != nil {
		return nil, err
	}
	secret, err := as.ECDH(ua)
	if err != nil {
		return nil, err
	}
	asPub := as.PublicKey().Bytes()
	// IKM = HKDF(auth, ecdh_secret, "WebPush: info" 0x00 ua_public as_public, 32)
	info := append(append([]byte("WebPush: info\x00"), p256dh...), asPub...)
	ikm, err := hkdf.Key(sha256.New, secret, auth, string(info), 32)
	if err != nil {
		return nil, err
	}
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	cek := expand(prk, "Content-Encoding: aes128gcm\x00", 16)
	nonce := expand(prk, "Content-Encoding: nonce\x00", 12)
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	// One record, the last: the payload followed by the delimiter 0x02.
	header := make([]byte, 0, 16+4+1+len(asPub))
	header = append(header, salt...)
	header = binary.BigEndian.AppendUint32(header, recordSize)
	header = append(header, byte(len(asPub)))
	header = append(header, asPub...)
	return gcm.Seal(header, nonce, append(append([]byte{}, plaintext...), 2), nil), nil
}

// Decrypt is the side of the browser: it opens a message for the key pair ua and the auth
// secret (the tests read what the pod sent).
func Decrypt(ua *ecdh.PrivateKey, auth, msg []byte) ([]byte, error) {
	if len(msg) < 86+17 || msg[20] != 65 {
		return nil, errors.New("push: bad header")
	}
	salt, asPub, body := msg[:16], msg[21:86], msg[86:]
	pub, err := ecdh.P256().NewPublicKey(asPub)
	if err != nil {
		return nil, err
	}
	secret, err := ua.ECDH(pub)
	if err != nil {
		return nil, err
	}
	info := append(append([]byte("WebPush: info\x00"), ua.PublicKey().Bytes()...), asPub...)
	ikm, err := hkdf.Key(sha256.New, secret, auth, string(info), 32)
	if err != nil {
		return nil, err
	}
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(expand(prk, "Content-Encoding: aes128gcm\x00", 16))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, expand(prk, "Content-Encoding: nonce\x00", 12), body, nil)
	if err != nil {
		return nil, err
	}
	if len(plain) == 0 || plain[len(plain)-1] != 2 {
		return nil, errors.New("push: no delimiter")
	}
	return plain[:len(plain)-1], nil
}

// expand is HKDF-Expand for one block (the lengths here are under 32 bytes).
func expand(prk []byte, info string, n int) []byte {
	m := hmac.New(sha256.New, prk)
	m.Write([]byte(info))
	m.Write([]byte{1})
	return m.Sum(nil)[:n]
}
