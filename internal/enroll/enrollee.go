package enroll

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type EnrolleeCallbacks struct {
	// MakeOffer returns the enrollee's public material for the granted role.
	MakeOffer func(role RoleOffer) ([]byte, error)
	// ShowSAS displays the short authentication string; the human at this end
	// reads it aloud to the issuer, who approves only on exact match.
	ShowSAS func(sas string)
}

// RunEnrollee drives the enrollee side: start (PAKE), offer (public material),
// then polls result until granted, denied, or ctx ends. Transport errors while
// polling are treated as transient — the issuer reloads the relay's Caddy
// mid-flow — and retried until the deadline.
//
// Callers (e.g. `tw join`) commonly pass a bare context.Background() with no
// ceiling of their own, so an abandoned exchange — the issuer never approves,
// or vanishes entirely — would otherwise poll /result forever: pollResult
// treats transport errors and non-2xx/404 responses as transient by design,
// precisely so a healthy exchange survives the issuer's own mid-flow Caddy
// reload. To bound that, RunEnrollee derives its own generous deadline here,
// comfortably above any invite TTL in practice, so a healthy exchange never
// hits it but an abandoned one exits with a clear error instead of hanging.
func RunEnrollee(ctx context.Context, httpc *http.Client, baseURL, code, tok string, cb EnrolleeCallbacks) (RoleOffer, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	var role RoleOffer
	sess := NewSession(Enrollee, code, tok)

	body, _ := json.Marshal(startReq{Pake: base64.StdEncoding.EncodeToString(sess.Start())})
	var sr startResp
	if err := postJSON(ctx, httpc, baseURL+"/start", body, &sr); err != nil {
		return role, nil, fmt.Errorf("redeeming invite: %w", err)
	}
	peer, err := base64.StdEncoding.DecodeString(sr.Pake)
	if err != nil {
		return role, nil, fmt.Errorf("bad issuer pake message: %w", err)
	}
	if err := sess.Finish(peer); err != nil {
		return role, nil, err
	}
	roleBox, err := base64.StdEncoding.DecodeString(sr.Box)
	if err != nil {
		return role, nil, fmt.Errorf("bad role box: %w", err)
	}
	rolePlain, err := sess.Open(roleBox)
	if err != nil {
		return role, nil, fmt.Errorf("code did not match the issuer's invite: %w", err)
	}
	if err := json.Unmarshal(rolePlain, &role); err != nil {
		return role, nil, fmt.Errorf("parsing role offer: %w", err)
	}

	offer, err := cb.MakeOffer(role)
	if err != nil {
		return role, nil, err
	}
	offerBox, err := sess.Seal(offer)
	if err != nil {
		return role, nil, err
	}
	body, _ = json.Marshal(boxMsg{Box: base64.StdEncoding.EncodeToString(offerBox)})
	if err := postJSON(ctx, httpc, baseURL+"/offer", body, nil); err != nil {
		return role, nil, fmt.Errorf("sending enrollment offer: %w", err)
	}
	cb.ShowSAS(sess.SAS(offer))

	for {
		grant, done, err := pollResult(ctx, httpc, baseURL+"/result")
		if err != nil {
			return role, nil, err
		}
		if done {
			plain, err := sess.Open(grant)
			if err != nil {
				return role, nil, fmt.Errorf("opening grant: %w", err)
			}
			return role, plain, nil
		}
		select {
		case <-ctx.Done():
			return role, nil, fmt.Errorf("issuer went away or enrollment not approved in time: %w", ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
}

func postJSON(ctx context.Context, httpc *http.Client, url string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusGone {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("issuer refused: %s", bytes.TrimSpace(msg))
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("issuer returned %s", resp.Status)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// pollResult returns (grantBox, true, nil) when granted, (nil, false, nil) to
// keep polling — including on transient transport errors — and a terminal
// error on denial.
func pollResult(ctx context.Context, httpc *http.Client, url string) ([]byte, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, err
	}
	resp, err := httpc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		return nil, false, nil // transient (Caddy reload) — keep polling
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil, false, nil
	case http.StatusOK:
		var m boxMsg
		if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
			return nil, false, err
		}
		b, err := base64.StdEncoding.DecodeString(m.Box)
		return b, true, err
	case http.StatusGone:
		return nil, false, fmt.Errorf("issuer denied the enrollment (SAS mismatch, expiry, or a burned invite)")
	default:
		return nil, false, nil // 404 during reload window — transient
	}
}
