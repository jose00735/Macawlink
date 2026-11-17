// Package pairing holds the PC's TLS identity and the phones paired over Wi-Fi.
//
// Pairing is numeric comparison with commitment (the Bluetooth/ZRTP idea): the PC commits to
// a random nonce, the phone sends its nonce, the PC reveals, and both show
// SAS(certFP, pcNonce, phoneNonce). A man in the middle presents a different certificate, so
// the two codes differ and the user rejects. Both sides must confirm. A paired phone gets a
// random token; it pins the PC certificate and presents the token on reconnect.
package pairing

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Identity is the PC's long-lived self-signed TLS certificate.
type Identity struct {
	Cert        tls.Certificate
	Fingerprint []byte // SHA-256 of the leaf DER
}

// LoadOrCreateIdentity reads dir/tls.pem or generates an ECDSA P-256 certificate.
func LoadOrCreateIdentity(dir, name string) (*Identity, error) {
	p := filepath.Join(dir, "tls.pem")
	if b, err := os.ReadFile(p); err == nil {
		if c, err := tls.X509KeyPair(b, b); err == nil && len(c.Certificate) > 0 {
			return identity(c), nil
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "Macawlink " + name, Organization: []string{"Macawlink"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(20, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	kb, _ := x509.MarshalECPrivateKey(key)
	out := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	out = append(out, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})...)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(p, out, 0o600); err != nil {
		return nil, err
	}
	c, err := tls.X509KeyPair(out, out)
	if err != nil {
		return nil, err
	}
	return identity(c), nil
}

func identity(c tls.Certificate) *Identity {
	sum := sha256.Sum256(c.Certificate[0])
	return &Identity{Cert: c, Fingerprint: sum[:]}
}

// ShortFP is shown in mDNS TXT and the UI so users can tell PCs apart.
func (id *Identity) ShortFP() string { return hex.EncodeToString(id.Fingerprint[:4]) }

// TLSConfig for the Wi-Fi listener: TLS 1.3 only.
func (id *Identity) TLSConfig() *tls.Config {
	return &tls.Config{Certificates: []tls.Certificate{id.Cert}, MinVersion: tls.VersionTLS13}
}

// Phone is one paired device.
type Phone struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	TokenHash string    `json:"token_hash"` // hex SHA-256 of the token; the token itself is not stored
	PairedAt  time.Time `json:"paired_at"`
	LastSeen  time.Time `json:"last_seen"`
}

// Store persists paired phones in dir/paired.json.
type Store struct {
	mu     sync.Mutex
	path   string
	phones map[string]*Phone
}

func OpenStore(dir string) *Store {
	s := &Store{phones: map[string]*Phone{}}
	if dir != "" {
		s.path = filepath.Join(dir, "paired.json")
		if b, err := os.ReadFile(s.path); err == nil {
			var list []*Phone
			if json.Unmarshal(b, &list) == nil {
				for _, p := range list {
					s.phones[p.ID] = p
				}
			}
		}
	}
	return s
}

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

// Check reports whether token is valid for phoneID, and records the visit.
func (s *Store) Check(phoneID, token string) bool {
	if phoneID == "" || token == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.phones[phoneID]
	if !ok || subtle.ConstantTimeCompare([]byte(p.TokenHash), []byte(hashToken(token))) != 1 {
		return false
	}
	p.LastSeen = time.Now()
	s.saveLocked()
	return true
}

// Add pairs a phone and returns its new token.
func (s *Store) Add(phoneID, name string) (string, error) {
	if phoneID == "" {
		return "", errors.New("pairing: empty phone id")
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.phones[phoneID] = &Phone{ID: phoneID, Name: name, TokenHash: hashToken(tok), PairedAt: now, LastSeen: now}
	return tok, s.saveLocked()
}

func (s *Store) Remove(phoneID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.phones, phoneID)
	s.saveLocked()
}

func (s *Store) List() []Phone {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Phone, 0, len(s.phones))
	for _, p := range s.phones {
		q := *p
		q.TokenHash = ""
		out = append(out, q)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PairedAt.Before(out[j].PairedAt) })
	return out
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	list := make([]*Phone, 0, len(s.phones))
	for _, p := range s.phones {
		list = append(list, p)
	}
	b, _ := json.MarshalIndent(list, "", "  ")
	os.MkdirAll(filepath.Dir(s.path), 0o700)
	return os.WriteFile(s.path, b, 0o600)
}

// NewNonce returns 32 random bytes.
func NewNonce() []byte {
	b := make([]byte, 32)
	rand.Read(b)
	return b
}
