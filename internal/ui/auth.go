package ui

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

// LinkTTL is how long an unused link can be traded, and how long a used
// one can still end the session it made (decision 0017).
const LinkTTL = 5 * time.Minute

// secretBytes of randomness in every link and session secret.
const secretBytes = 32

var (
	errNoLink     = errors.New("this link isn't one tailsuite up gave out")
	errExpired    = errors.New("this link has expired: press Enter in the terminal running tailsuite up for a fresh one")
	errReused     = errors.New("this link was used twice, so the session it opened has ended: press Enter in the terminal running tailsuite up for a fresh one")
	errNoSession  = errors.New("no session: open the link tailsuite up printed")
	errLinkIsDead = errors.New("this link has been used: press Enter in the terminal running tailsuite up for a fresh one")
)

// link is one one-time link.
type link struct {
	created time.Time
	session string // the session its trade made; "" until traded
	dead    bool   // traded twice: refused from now on
	traded  chan struct{}
}

// auth holds the links and sessions. Secrets are kept only as their
// SHA-256, so a map lookup reveals nothing about one by its timing.
type auth struct {
	now func() time.Time

	mu       sync.Mutex
	links    map[[32]byte]*link
	sessions map[[32]byte]bool
}

func newAuth(now func() time.Time) *auth {
	return &auth{now: now, links: map[[32]byte]*link{}, sessions: map[[32]byte]bool{}}
}

func newSecret() (string, error) {
	b := make([]byte, secretBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func key(secret string) [32]byte { return sha256.Sum256([]byte(secret)) }

// newLink makes a link's secret, and a channel closed when it's traded.
func (a *auth) newLink() (string, <-chan struct{}, error) {
	s, err := newSecret()
	if err != nil {
		return "", nil, err
	}
	l := &link{created: a.now(), traded: make(chan struct{})}
	a.mu.Lock()
	a.links[key(s)] = l
	a.mu.Unlock()
	return s, l.traded, nil
}

// trade swaps a link's secret for a session secret, once. A used link
// traded again within its lifetime ends the session the first trade made
// (someone else may have it); after that, or after its lifetime, it's
// refused.
func (a *auth) trade(secret string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	l, ok := a.links[key(secret)]
	switch {
	case !ok || secret == "":
		return "", errNoLink
	case l.dead:
		return "", errLinkIsDead
	case a.now().Sub(l.created) > LinkTTL:
		return "", errExpired
	case l.session != "":
		delete(a.sessions, key(l.session))
		l.dead = true
		return "", errReused
	}
	s, err := newSecret()
	if err != nil {
		return "", err
	}
	a.sessions[key(s)] = true
	l.session = s
	close(l.traded)
	return s, nil
}

// valid says whether a session secret is live.
func (a *auth) valid(session string) bool {
	if session == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessions[key(session)]
}
