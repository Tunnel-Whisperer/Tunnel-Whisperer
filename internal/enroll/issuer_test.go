package enroll

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

type enrolleeResult struct {
	grant []byte
	err   error
}

// startExchange serves a fresh invite's Handler and runs the real enrollee
// against it with the right code. It returns once the issuer holds the
// enrollee's offer and the enrollee's first (pre-grant) /result poll is
// answered, so the enrollee is in its 2s poll sleep.
func startExchange(t *testing.T) (*Handler, string, <-chan enrolleeResult) {
	t.Helper()
	inv, err := Mint("abcd1234", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(inv, RoleOffer{Role: "server"})
	polled := make(chan struct{}, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r)
		if strings.HasSuffix(r.URL.Path, "/result") && r.Header.Get(resultProofHeader) != "" {
			select {
			case polled <- struct{}{}:
			default:
			}
		}
	}))
	t.Cleanup(srv.Close)
	base := srv.URL + "/enroll/abcd1234"

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	done := make(chan enrolleeResult, 1)
	go func() {
		_, grant, err := RunEnrollee(ctx, srv.Client(), base, inv.Code, inv.Tok, EnrolleeCallbacks{
			MakeOffer: func(RoleOffer) ([]byte, error) { return []byte(`{"offer":1}`), nil },
			ShowSAS:   func(string) {},
		})
		done <- enrolleeResult{grant, err}
	}()
	if _, err := h.AwaitOffer(ctx); err != nil {
		t.Fatalf("AwaitOffer: %v", err)
	}
	select {
	case <-polled:
	case <-ctx.Done():
		t.Fatal("enrollee never polled /result")
	}
	return h, base, done
}

func waitEnrollee(t *testing.T, done <-chan enrolleeResult) enrolleeResult {
	t.Helper()
	select {
	case r := <-done:
		return r
	case <-time.After(15 * time.Second):
		t.Fatal("enrollee did not finish")
		return enrolleeResult{}
	}
}

func awaitCollected(h *Handler, d time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	return h.AwaitCollected(ctx)
}

func TestHandlerHappyPath(t *testing.T) {
	h, _, done := startExchange(t)
	if err := h.Grant([]byte("the-grant")); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if err := awaitCollected(h, 10*time.Second); err != nil {
		t.Fatalf("AwaitCollected: %v", err)
	}
	r := waitEnrollee(t, done)
	if r.err != nil || string(r.grant) != "the-grant" {
		t.Fatalf("enrollee: grant=%q err=%v", r.grant, r.err)
	}
}

// TestHandlerGarbageOfferAfterValidDoesNotDeny: the route token is not a
// secret, so a box that fails to open after the real offer is in proves
// nothing about the enrollee and must not abort the enrollment (C6).
func TestHandlerGarbageOfferAfterValidDoesNotDeny(t *testing.T) {
	h, base, done := startExchange(t)
	resp, err := http.Post(base+"/offer", "application/json", strings.NewReader(`{"box":"AAAA"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusGone {
		t.Fatalf("garbage /offer status = %d, want 410", resp.StatusCode)
	}
	if h.Denied() {
		t.Fatal("garbage /offer after a valid offer denied the enrollment")
	}
	if err := h.Grant([]byte("the-grant")); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	r := waitEnrollee(t, done)
	if r.err != nil || string(r.grant) != "the-grant" {
		t.Fatalf("enrollee: grant=%q err=%v", r.grant, r.err)
	}
}

// TestHandlerWrongKeyOfferBeforeValidDenies keeps the wrong-code verdict: a
// box that does not open under the session key, with no valid offer yet,
// denies the exchange, and Grant then refuses.
func TestHandlerWrongKeyOfferBeforeValidDenies(t *testing.T) {
	inv, err := Mint("abcd1234", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(inv, RoleOffer{Role: "server"})
	srv := httptest.NewServer(h)
	defer srv.Close()

	wrong := NewSession(Enrollee, inv.Code+"-wrong", inv.Tok)
	body, _ := json.Marshal(startReq{Pake: base64.StdEncoding.EncodeToString(wrong.Start())})
	resp, err := http.Post(srv.URL+"/start", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var sr startResp
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	peer, _ := base64.StdEncoding.DecodeString(sr.Pake)
	if err := wrong.Finish(peer); err != nil {
		t.Fatal(err)
	}
	box, err := wrong.Seal([]byte(`{"offer":1}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ = json.Marshal(boxMsg{Box: base64.StdEncoding.EncodeToString(box)})
	resp, err = http.Post(srv.URL+"/offer", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusGone || !h.Denied() {
		t.Fatalf("wrong-key offer: status %d denied %v, want 410 and denied", resp.StatusCode, h.Denied())
	}
	if err := h.Grant([]byte("the-grant")); err == nil {
		t.Fatal("Grant succeeded on a denied exchange")
	}
}

// TestHandlerUnauthenticatedResultDoesNotComplete: a /result poll without a
// session-key proof (or with a forged one) may read the sealed grant but must
// not tell the issuer the enrollee collected it (C6).
func TestHandlerUnauthenticatedResultDoesNotComplete(t *testing.T) {
	h, base, done := startExchange(t)
	if err := h.Grant([]byte("the-grant")); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	for _, proof := range []string{"", "AAAA", base64.StdEncoding.EncodeToString(make([]byte, 64))} {
		req, _ := http.NewRequest(http.MethodGet, base+"/result", nil)
		if proof != "" {
			req.Header.Set(resultProofHeader, proof)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("proof %q: /result status = %d, want 200", proof, resp.StatusCode)
		}
	}
	if err := awaitCollected(h, 300*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unproven /result completed collection: AwaitCollected = %v", err)
	}
	r := waitEnrollee(t, done)
	if r.err != nil || string(r.grant) != "the-grant" {
		t.Fatalf("enrollee: grant=%q err=%v", r.grant, r.err)
	}
	if err := awaitCollected(h, 5*time.Second); err != nil {
		t.Fatalf("enrollee's proven poll did not complete collection: %v", err)
	}
}

// manualExchange serves a fresh invite's Handler and completes /start with
// the right code, leaving the caller to drive /offer and /result by hand the
// way a given enrollee version would.
func manualExchange(t *testing.T) (*Handler, string, *Session) {
	t.Helper()
	inv, err := Mint("abcd1234", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(inv, RoleOffer{Role: "server"})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	sess := NewSession(Enrollee, inv.Code, inv.Tok)
	body, _ := json.Marshal(startReq{Pake: base64.StdEncoding.EncodeToString(sess.Start())})
	resp, err := http.Post(srv.URL+"/start", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var sr startResp
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	peer, _ := base64.StdEncoding.DecodeString(sr.Pake)
	if err := sess.Finish(peer); err != nil {
		t.Fatal(err)
	}
	return h, srv.URL, sess
}

func postOffer(t *testing.T, base string, sess *Session, plain []byte) int {
	t.Helper()
	box, err := sess.Seal(plain)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(boxMsg{Box: base64.StdEncoding.EncodeToString(box)})
	resp, err := http.Post(base+"/offer", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func getResult(t *testing.T, base string, sess *Session) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, base+"/result", nil)
	if sess != nil {
		box, err := sess.Seal([]byte(resultProofPlaintext))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(resultProofHeader, base64.StdEncoding.EncodeToString(box))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// TestHandlerLegacyEnrolleeResultCompletes: an enrollee that predates the
// result proof sends an unmarked offer and polls /result without the header;
// its poll must still complete collection, or the issuer reports a grant the
// enrollee holds as never collected.
func TestHandlerLegacyEnrolleeResultCompletes(t *testing.T) {
	h, base, sess := manualExchange(t)
	if st := postOffer(t, base, sess, []byte(`{"offer":1}`)); st != http.StatusNoContent {
		t.Fatalf("/offer status = %d, want 204", st)
	}
	payload, err := h.AwaitOffer(context.Background())
	if err != nil || string(payload) != `{"offer":1}` {
		t.Fatalf("AwaitOffer = %q, %v", payload, err)
	}
	if err := h.Grant([]byte("the-grant")); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if st := getResult(t, base, nil); st != http.StatusOK {
		t.Fatalf("/result status = %d, want 200", st)
	}
	if err := awaitCollected(h, 5*time.Second); err != nil {
		t.Fatalf("legacy enrollee's /result did not complete collection: %v", err)
	}
}

// TestHandlerV2OfferCannotBeDowngraded: once the first valid offer announced
// result proofs, neither a later unmarked offer (even a valid one) nor a
// garbage one lets an unproven /result complete collection.
func TestHandlerV2OfferCannotBeDowngraded(t *testing.T) {
	h, base, sess := manualExchange(t)
	marked, err := markOfferV2([]byte(`{"offer":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if st := postOffer(t, base, sess, marked); st != http.StatusNoContent {
		t.Fatalf("v2 /offer status = %d, want 204", st)
	}
	payload, err := h.AwaitOffer(context.Background())
	if err != nil || string(payload) != `{"offer":1}` {
		t.Fatalf("AwaitOffer = %q, %v", payload, err)
	}
	if got, want := h.SAS(), sess.SAS(marked); got != want {
		t.Fatalf("issuer SAS %q != enrollee SAS %q", got, want)
	}
	if st := postOffer(t, base, sess, []byte(`{"offer":1}`)); st != http.StatusNoContent {
		t.Fatalf("second /offer status = %d, want 204", st)
	}
	resp, err := http.Post(base+"/offer", "application/json", strings.NewReader(`{"box":"AAAA"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if err := h.Grant([]byte("the-grant")); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if st := getResult(t, base, nil); st != http.StatusOK {
		t.Fatalf("unproven /result status = %d, want 200", st)
	}
	if err := awaitCollected(h, 300*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unproven /result after a v2 offer completed collection: AwaitCollected = %v", err)
	}
	if st := getResult(t, base, sess); st != http.StatusOK {
		t.Fatalf("proven /result status = %d, want 200", st)
	}
	if err := awaitCollected(h, 5*time.Second); err != nil {
		t.Fatalf("proven /result did not complete collection: %v", err)
	}
}

func TestMarkOfferV2RoundTrip(t *testing.T) {
	for _, in := range []string{`{}`, `{"a":1}`, ` { "a" : [1,2] } `} {
		marked, err := markOfferV2([]byte(in))
		if err != nil {
			t.Fatalf("mark %q: %v", in, err)
		}
		if !json.Valid(marked) {
			t.Fatalf("marked %q is not valid JSON: %s", in, marked)
		}
		out, v2 := unmarkOfferV2(marked)
		var a, b any
		_ = json.Unmarshal([]byte(in), &a)
		if err := json.Unmarshal(out, &b); err != nil || !v2 || !reflect.DeepEqual(a, b) {
			t.Fatalf("unmark(mark(%q)) = %s, v2=%v, err=%v", in, out, v2, err)
		}
	}
	for _, in := range []string{``, `[]`, `"x"`, `{`} {
		if _, err := markOfferV2([]byte(in)); err == nil {
			t.Fatalf("mark %q: want error", in)
		}
	}
	for _, in := range []string{`{"a":1}`, `{"tw_enroll_v":20}`, `{"tw_enroll_v":2`} {
		if _, v2 := unmarkOfferV2([]byte(in)); v2 {
			t.Fatalf("unmark %q reported v2", in)
		}
	}
}
