package headscale

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"
)

// labTLS is a run's own certificate authority and headscale's certificate
// from it (decision 0003: TLS in the lab). The CA's key exists only while
// labTLS is made: it signs one certificate and is dropped, so nothing can
// issue another.
type labTLS struct {
	CA   []byte // PEM, trusted by every node and by nothing else
	Cert []byte // PEM, for host
	Key  []byte // PEM, headscale's key
}

// certValidity is long enough for a lab that `up` leaves running. A
// longer one costs nothing: the CA's key is gone.
const certValidity = 365 * 24 * time.Hour

func newLabTLS(host string, now time.Time) (labTLS, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return labTLS{}, fmt.Errorf("lab CA: %w", err)
	}
	serial := func() (*big.Int, error) { return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127)) }
	caSerial, err := serial()
	if err != nil {
		return labTLS{}, fmt.Errorf("lab CA: %w", err)
	}
	notBefore, notAfter := now.Add(-time.Hour), now.Add(certValidity)
	caTmpl := &x509.Certificate{
		SerialNumber:          caSerial,
		Subject:               pkix.Name{CommonName: "tailsuite lab CA"},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
		// It can only ever vouch for the control plane's name.
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         []string{host},
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return labTLS{}, fmt.Errorf("lab CA: %w", err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		return labTLS{}, fmt.Errorf("lab CA: %w", err)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return labTLS{}, fmt.Errorf("lab certificate: %w", err)
	}
	certSerial, err := serial()
	if err != nil {
		return labTLS{}, fmt.Errorf("lab certificate: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: certSerial,
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return labTLS{}, fmt.Errorf("lab certificate: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return labTLS{}, fmt.Errorf("lab certificate: %w", err)
	}
	return labTLS{
		CA:   pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		Cert: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		Key:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	}, nil
}
