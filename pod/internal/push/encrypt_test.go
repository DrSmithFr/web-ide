package push

import (
	"bytes"
	"crypto/ecdh"
	"encoding/base64"
	"testing"
)

func b64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestEncryptRFC8291 checks the encryption against the example of RFC 8291, Appendix A.
func TestEncryptRFC8291(t *testing.T) {
	plaintext := b64(t, "V2hlbiBJIGdyb3cgdXAsIEkgd2FudCB0byBiZSBhIHdhdGVybWVsb24")
	asPriv := b64(t, "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw")
	uaPub := b64(t, "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4")
	salt := b64(t, "DGv6ra1nlYgDCS1FRnbzlw")
	auth := b64(t, "BTBZMqHH6r4Tts7J_aSIgg")
	want := b64(t, "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN")
	as, err := ecdh.P256().NewPrivateKey(asPriv)
	if err != nil {
		t.Fatal(err)
	}
	got, err := encrypt(as, salt, uaPub, auth, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("the RFC 8291 example:\n got %s\nwant %s", base64.RawURLEncoding.EncodeToString(got), base64.RawURLEncoding.EncodeToString(want))
	}
	ua, err := ecdh.P256().NewPrivateKey(b64(t, "q1dXpw3UpT5VOmu_cf_v6ih07Aems3njxI-JWgLcM94"))
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := Decrypt(ua, auth, want); err != nil || !bytes.Equal(plain, plaintext) {
		t.Fatalf("the browser side of the example: %q %v", plain, err)
	}
}

// TestEncryptRoundTrip decrypts a fresh message as a browser does.
func TestEncryptRoundTrip(t *testing.T) {
	ua, _ := ecdh.P256().GenerateKey(nil)
	auth := bytes.Repeat([]byte{7}, 16)
	msg, err := Encrypt(ua.PublicKey().Bytes(), auth, []byte(`{"title":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := Decrypt(ua, auth, msg); err != nil || string(plain) != `{"title":"x"}` {
		t.Fatalf("decrypt: %q %v", plain, err)
	}
	if _, err := Decrypt(ua, bytes.Repeat([]byte{8}, 16), msg); err == nil {
		t.Fatal("another auth secret cannot decrypt")
	}
}
