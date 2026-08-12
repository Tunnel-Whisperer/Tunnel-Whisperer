package enroll

import (
	"crypto/rand"
	_ "embed"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"
)

//go:embed words.txt
var wordsFile string

// words are the first 256 entries of the public-domain BIP-0039 English
// wordlist: curated so every pair differs in the first four letters, which
// keeps a spoken or typed invite code unambiguous.
var words = strings.Fields(wordsFile)

// Invite is one single-use, short-TTL enrollment offer. Code format:
// "<tok>-NN-word-word" — tok routes /enroll/<tok> on the relay and the FULL
// string is the SPAKE2 password. Low entropy is safe: a PAKE password cannot
// be guessed offline and the invite burns on the first redemption attempt.
type Invite struct {
	Code    string
	Tok     string
	Expires time.Time

	mu       sync.Mutex
	redeemed bool
}

func Mint(issuerTok string, ttl time.Duration) (*Invite, error) {
	if len(words) != 256 {
		return nil, fmt.Errorf("embedded wordlist has %d words, want 256", len(words))
	}
	idx := make([]int64, 3)
	for i, max := range []int64{100, 256, 256} {
		n, err := rand.Int(rand.Reader, big.NewInt(max))
		if err != nil {
			return nil, fmt.Errorf("generating invite code: %w", err)
		}
		idx[i] = n.Int64()
	}
	return &Invite{
		Code:    fmt.Sprintf("%s-%02d-%s-%s", issuerTok, idx[0], words[idx[1]], words[idx[2]]),
		Tok:     issuerTok,
		Expires: time.Now().Add(ttl),
	}, nil
}

func (i *Invite) Expired() bool { return time.Now().After(i.Expires) }

// Redeem burns the invite: only the FIRST call while unexpired succeeds.
// Any later attempt — including one with a wrong code that reached /start —
// finds it burned.
func (i *Invite) Redeem() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.redeemed {
		return fmt.Errorf("invite already used")
	}
	if i.Expired() {
		return fmt.Errorf("invite expired")
	}
	i.redeemed = true
	return nil
}

// ParseCode extracts the issuer token (the /enroll route) from a spoken code.
func ParseCode(code string) (string, error) {
	parts := strings.Split(strings.TrimSpace(code), "-")
	if len(parts) != 4 || parts[0] == "" {
		return "", fmt.Errorf("invite code must look like tok-NN-word-word")
	}
	return parts[0], nil
}
