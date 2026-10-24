package push

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"
	"golang.org/x/crypto/hkdf"
)

// TestNotifyUsesAppNameAsTheTitle goes through Notify. A mutant that ignores
// Config.AppName still builds a payload, and only the decrypted body shows it.
func TestNotifyUsesAppNameAsTheTitle(t *testing.T) {
	privateKey, publicKey, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	authSecret := make([]byte, 16)
	if _, err := rand.Read(authSecret); err != nil {
		t.Fatal(err)
	}
	subs := newTestSubscriptions(t)
	scope := testScope(t, subs)
	sub := Subscription{
		Endpoint: "https://updates.push.services.mozilla.com/wpush/v2/northwind",
		P256dh:   base64.RawURLEncoding.EncodeToString(receiver.PublicKey().Bytes()),
		Auth:     base64.RawURLEncoding.EncodeToString(authSecret),
	}
	if err := subs.Save(context.Background(), scope, sub); err != nil {
		t.Fatal(err)
	}
	captured := &captureTransport{}
	previous := pushClient
	pushClient = &http.Client{Transport: captured}
	t.Cleanup(func() { pushClient = previous })

	NewSender(subs, Config{
		PublicKey:  publicKey,
		PrivateKey: privateKey,
		Subject:    "mailto:pantry@localhost",
		AppName:    "Northwind",
	}).Notify(context.Background(), scope, Notice{URL: "/"})

	if len(captured.body) == 0 {
		t.Fatal("Notify did not send a push body")
	}
	plain, err := decryptWebPush(captured.body, receiver, authSecret)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plain), `"title":"Northwind"`) {
		t.Fatalf("payload = %s", plain)
	}
}

type captureTransport struct {
	body []byte
}

func (c *captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	c.body = body
	return &http.Response{
		StatusCode: http.StatusCreated,
		Body:       io.NopCloser(bytes.NewReader(nil)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

func decryptWebPush(record []byte, receiver *ecdh.PrivateKey, authSecret []byte) ([]byte, error) {
	if len(record) < 16+4+1 {
		return nil, io.ErrUnexpectedEOF
	}
	salt := record[:16]
	idLen := int(record[20])
	header := 21 + idLen
	if len(record) < header {
		return nil, io.ErrUnexpectedEOF
	}
	serverPub := record[21:header]
	ciphertext := record[header:]
	x, y := elliptic.Unmarshal(elliptic.P256(), serverPub)
	if x == nil {
		return nil, io.ErrUnexpectedEOF
	}
	sx, _ := elliptic.P256().ScalarMult(x, y, receiver.Bytes())
	shared := make([]byte, 32)
	sx.FillBytes(shared)
	info := append([]byte("WebPush: info\x00"), receiver.PublicKey().Bytes()...)
	info = append(info, serverPub...)
	ikm, err := readHKDF(shared, authSecret, info, 32)
	if err != nil {
		return nil, err
	}
	cek, err := readHKDF(ikm, salt, []byte("Content-Encoding: aes128gcm\x00"), 16)
	if err != nil {
		return nil, err
	}
	nonce, err := readHKDF(ikm, salt, []byte("Content-Encoding: nonce\x00"), 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}
	end := len(plain) - 1
	for end >= 0 && plain[end] == 0 {
		end--
	}
	if end >= 0 && plain[end] == 2 {
		plain = plain[:end]
	}
	return plain, nil
}

func readHKDF(secret, salt, info []byte, n int) ([]byte, error) {
	r := hkdf.New(sha256.New, secret, salt, info)
	out := make([]byte, n)
	if _, err := io.ReadFull(r, out); err != nil {
		return nil, err
	}
	return out, nil
}
