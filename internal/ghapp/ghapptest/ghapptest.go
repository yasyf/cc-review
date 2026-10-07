// Package ghapptest installs a cc-review GitHub App for tests without the
// manifest flow, backed by the in-memory mock keyring.
package ghapptest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/yasyf/cc-review/internal/ghapp"
)

// Install switches the keyring to its in-memory mock and saves app with a
// fresh RSA key under the test's pinned HOME, returning the key.
func Install(t testing.TB, app ghapp.App) *rsa.PrivateKey {
	t.Helper()
	keyring.MockInit()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	if err := ghapp.Save(app, PEM(key)); err != nil {
		t.Fatal(err)
	}
	return key
}

// PEM encodes key the way GitHub's manifest conversion returns it.
func PEM(key *rsa.PrivateKey) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
}
