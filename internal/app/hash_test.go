package app

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestHashUserID_DeterministicAndSalted(t *testing.T) {
	t.Parallel()

	a := &App{voteSalt: "pepper"}

	got := a.hashUserID(123)
	want := sha256hex("pepper:123")
	if got != want {
		t.Fatalf("hash mismatch\ngot : %s\nwant: %s", got, want)
	}

	// same input -> same output
	if got2 := a.hashUserID(123); got2 != got {
		t.Fatalf("not deterministic: %s vs %s", got, got2)
	}

	// different salt -> different hash
	b := &App{voteSalt: "different"}
	if b.hashUserID(123) == got {
		t.Fatalf("salt does not affect hash")
	}

	// different user -> different hash
	if a.hashUserID(124) == got {
		t.Fatalf("user id does not affect hash")
	}
}
