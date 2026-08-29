package enroll

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Handler serves one invite's exchange on the issuer side. It routes by path
// SUFFIX (/start, /offer, /result) so it works mounted behind any prefix.
type Handler struct {
	invite *Invite
	role   RoleOffer

	mu        sync.Mutex
	sess      *Session
	offer     []byte // decrypted offer payload
	grant     []byte // sealed grant box
	denied    bool
	collected bool
	offerCh   chan struct{}
	collectCh chan struct{}
}

func NewHandler(inv *Invite, role RoleOffer) *Handler {
	return &Handler{invite: inv, role: role,
		offerCh: make(chan struct{}), collectCh: make(chan struct{})}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/start"):
		h.handleStart(w, r)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/offer"):
		h.handleOffer(w, r)
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/result"):
		h.handleResult(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) handleStart(w http.ResponseWriter, r *http.Request) {
	var req startReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	peer, err := base64.StdEncoding.DecodeString(req.Pake)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	// Redeem FIRST: any well-formed start attempt — right or wrong code — burns
	// the invite. This is a DELIBERATE tradeoff (finding #9): burning on the
	// first attempt makes the short PAKE code single-guess, defeating online
	// brute-force of the code, at the cost of a cheap denial-of-service (a
	// stranger who reaches the endpoint can burn an unredeemed invite). We
	// accept the DoS because an invite is short-lived and re-issued in one
	// command (`tw relay invite` / `tw server user invite`), whereas allowing
	// repeated guesses would weaken the code's secrecy — the opposite priority.
	// (A malformed request is rejected above, before this, so it never burns.)
	if err := h.invite.Redeem(); err != nil {
		http.Error(w, err.Error(), http.StatusGone)
		return
	}
	sess := NewSession(Issuer, h.invite.Code, h.invite.Tok)
	msg := sess.Start()
	if err := sess.Finish(peer); err != nil {
		http.Error(w, "pake failed", http.StatusGone)
		return
	}
	rolePlain, err := json.Marshal(h.role)
	if err == nil {
		var box []byte
		if box, err = sess.Seal(rolePlain); err == nil {
			h.mu.Lock()
			h.sess = sess
			h.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(startResp{
				Pake: base64.StdEncoding.EncodeToString(msg),
				Box:  base64.StdEncoding.EncodeToString(box),
			})
			return
		}
	}
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func (h *Handler) handleOffer(w http.ResponseWriter, r *http.Request) {
	var req boxMsg
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	h.mu.Lock()
	sess := h.sess
	h.mu.Unlock()
	box, err := base64.StdEncoding.DecodeString(req.Box)
	if sess == nil || err != nil {
		http.Error(w, "no session", http.StatusGone)
		return
	}
	plain, err := sess.Open(box)
	if err != nil {
		// Wrong key = wrong code or tamper. The invite is already burned.
		h.Deny()
		http.Error(w, "rejected", http.StatusGone)
		return
	}
	h.mu.Lock()
	first := h.offer == nil && !h.denied
	if first {
		h.offer = plain
	}
	h.mu.Unlock()
	if first {
		close(h.offerCh)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleResult(w http.ResponseWriter, r *http.Request) {
	// The read of grant/denied and the collected check-and-set must happen in
	// a single critical section: reading `collected` in one lock and setting
	// it in a later, separate lock would let two concurrent /result polls
	// both observe collected==false and both call close(h.collectCh).
	h.mu.Lock()
	grant, denied := h.grant, h.denied
	expired := h.invite.Expired()
	firstCollect := grant != nil && !h.collected
	if firstCollect {
		h.collected = true
	}
	h.mu.Unlock()
	switch {
	case denied || expired && grant == nil:
		http.Error(w, "denied", http.StatusGone)
	case grant == nil:
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(boxMsg{Box: base64.StdEncoding.EncodeToString(grant)})
		if firstCollect {
			close(h.collectCh)
		}
	}
}

// AwaitOffer blocks until the enrollee's decrypted offer payload arrives.
func (h *Handler) AwaitOffer(ctx context.Context) ([]byte, error) {
	expire := time.NewTimer(time.Until(h.invite.Expires))
	defer expire.Stop()
	select {
	case <-h.offerCh:
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.offer, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-expire.C:
		return nil, fmt.Errorf("invite expired before an enrollment arrived")
	}
}

func (h *Handler) SAS() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sess == nil || h.offer == nil {
		return ""
	}
	return h.sess.SAS(h.offer)
}

func (h *Handler) Grant(plaintext []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sess == nil {
		return fmt.Errorf("no session")
	}
	box, err := h.sess.Seal(plaintext)
	if err != nil {
		return fmt.Errorf("sealing grant: %w", err)
	}
	h.grant = box
	return nil
}

func (h *Handler) Deny() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.denied = true
}

// AwaitCollected returns once the enrollee has fetched the grant (or ctx ends).
func (h *Handler) AwaitCollected(ctx context.Context) error {
	select {
	case <-h.collectCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
