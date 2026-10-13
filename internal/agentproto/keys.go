// Package agentproto holds what the Quicksand server and the in-guest agent
// share: key formats, TLS configuration, API types and agent addressing.
//
// Authentication is mutual TLS. The server owns an Ed25519 key; each agent
// is configured with the matching public key and only accepts TLS clients
// presenting a certificate for it. The agent's own certificate is generated
// at startup and not verified by the server (see ClientTLSConfig).
package agentproto

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

const publicKeyPrefix = "ed25519:"

// FormatPublicKey renders a public key as "ed25519:<base64>", the form
// pasted into agent configs.
func FormatPublicKey(pub ed25519.PublicKey) string {
	return publicKeyPrefix + base64.StdEncoding.EncodeToString(pub)
}

func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	b64, ok := strings.CutPrefix(strings.TrimSpace(s), publicKeyPrefix)
	if !ok {
		return nil, fmt.Errorf("public key must start with %q", publicKeyPrefix)
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, errors.New("invalid ed25519 public key")
	}
	return ed25519.PublicKey(raw), nil
}

const privateKeyPrefix = "ed25519-private:"

// GenerateKey returns a new private key.
func GenerateKey() (ed25519.PrivateKey, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	return priv, err
}

// FormatPrivateKey renders a private key as "ed25519-private:<base64 seed>",
// the form stored in Quicksand's config.
func FormatPrivateKey(priv ed25519.PrivateKey) string {
	return privateKeyPrefix + base64.StdEncoding.EncodeToString(priv.Seed())
}

func ParsePrivateKey(s string) (ed25519.PrivateKey, error) {
	b64, ok := strings.CutPrefix(strings.TrimSpace(s), privateKeyPrefix)
	if !ok {
		if strings.HasPrefix(strings.TrimSpace(s), publicKeyPrefix) {
			return nil, errors.New("this is a public key; the private key starts with " + privateKeyPrefix)
		}
		return nil, fmt.Errorf("private key must start with %q", privateKeyPrefix)
	}
	seed, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("invalid ed25519 private key")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// selfSigned makes a certificate for key; its only purpose is to carry the
// public key through the TLS handshake.
func selfSigned(key ed25519.PrivateKey, name string) (tls.Certificate, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: crypto.Signer(key)}, nil
}

// ClientTLSConfig is the server's side of a connection to an agent.
//
// The agent's certificate is deliberately not verified: the agent runs in
// an untrusted guest, so nothing it says is trusted anyway, and which agent
// answers at an address is guaranteed by the network (per-VM IP filters on
// an isolated segment), not by agent keys baked into shared templates.
func ClientTLSConfig(key ed25519.PrivateKey) (*tls.Config, error) {
	cert, err := selfSigned(key, "quicksand")
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS13,
	}, nil
}

// ServerTLSConfig is the agent's side: an ephemeral certificate, and only
// clients holding one of the authorized keys are accepted.
func ServerTLSConfig(authorized []ed25519.PublicKey) (*tls.Config, error) {
	if len(authorized) == 0 {
		return nil, errors.New("no authorized keys configured")
	}
	_, ephemeral, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	cert, err := selfSigned(ephemeral, "quicksand-agent")
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		// Chains aren't verified; the client's key is checked below.
		ClientAuth: tls.RequireAnyClientCert,
		MinVersion: tls.VersionTLS13,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return errors.New("no client certificate")
			}
			leaf, err := x509.ParseCertificate(rawCerts[0])
			if err != nil {
				return err
			}
			pub, ok := leaf.PublicKey.(ed25519.PublicKey)
			if !ok {
				return errors.New("client key is not ed25519")
			}
			for _, a := range authorized {
				if a.Equal(pub) {
					return nil
				}
			}
			return errors.New("client key is not authorized")
		},
	}, nil
}
