package ykt

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
)

// rsaPublicKeyPEM is the frontend-hardcoded public key for ykt password encryption.
// This is PKCS#1 v1.5 1024-bit key.
const rsaPublicKeyPEM = `-----BEGIN PUBLIC KEY-----
MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQCI6HjvsHyzquAmVxJD6X0dy1ZT
oaqpxsCNIdl2xvpoNTWivmhYlTWAkWivw7yDd5Nu6bwr2LTrb8FXs9zSoSm1rTfe
+OnH74717auj8DCo5UxtzHXDWZqhgGzmzDbKc/lZKLw94Zk5wzqDAU5lbefj9R+
ol6eROn66Wz9A49Pl1QIDAQAB
-----END PUBLIC KEY-----`

var publicKey *rsa.PublicKey

func init() {
	// Parse the public key once at init time
	block, _ := pem.Decode([]byte(rsaPublicKeyPEM))
	if block == nil {
		panic("failed to parse RSA public key PEM")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		panic("failed to parse RSA public key: " + err.Error())
	}

	var ok bool
	publicKey, ok = pub.(*rsa.PublicKey)
	if !ok {
		panic("not an RSA public key")
	}
}

// EncryptPassword encrypts a password using RSA PKCS#1 v1.5.
func EncryptPassword(password string) (string, error) {
	ciphertext, err := rsa.EncryptPKCS1v15(rand.Reader, publicKey, []byte(password))
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}
