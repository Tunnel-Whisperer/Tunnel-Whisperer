package enroll

import (
	"bytes"
	"testing"
)

func handshake(t *testing.T, codeA, codeB string) (*Session, *Session, error) {
	t.Helper()
	a := NewSession(Enrollee, codeA, "a1b2c3d4")
	b := NewSession(Issuer, codeB, "a1b2c3d4")
	msgA, msgB := a.Start(), b.Start()
	if err := a.Finish(msgB); err != nil {
		return nil, nil, err
	}
	if err := b.Finish(msgA); err != nil {
		return nil, nil, err
	}
	return a, b, nil
}

func TestSessionRoundTrip(t *testing.T) {
	a, b, err := handshake(t, "a1b2c3d4-47-ember-falcon", "a1b2c3d4-47-ember-falcon")
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	box, err := a.Seal([]byte("hello"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	pt, err := b.Open(box)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !bytes.Equal(pt, []byte("hello")) {
		t.Fatalf("got %q", pt)
	}
	// Same inputs on both sides -> same SAS; different offer -> different SAS.
	offer := []byte(`{"k":"v"}`)
	if a.SAS(offer) != b.SAS(offer) {
		t.Fatal("SAS mismatch on identical inputs")
	}
	if a.SAS(offer) == a.SAS([]byte(`{"k":"OTHER"}`)) {
		t.Fatal("SAS did not bind the offer payload")
	}
	if got := a.SAS(offer); len(got) != 7 || got[3] != '-' {
		t.Fatalf("SAS format: %q", got)
	}
}

func TestSessionWrongCode(t *testing.T) {
	a, b, err := handshake(t, "a1b2c3d4-47-ember-falcon", "a1b2c3d4-47-ember-WRONG")
	if err != nil {
		return // some PAKE impls fail at Finish — also acceptable
	}
	box, err := a.Seal([]byte("hello"))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if _, err := b.Open(box); err == nil {
		t.Fatal("box opened across mismatched codes")
	}
}
