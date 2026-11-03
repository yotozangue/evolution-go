package poll_service

import (
	"fmt"
	"testing"

	"go.mau.fi/whatsmeow"
)

func TestOptionHashMatchesWhatsmeow(t *testing.T) {
	options := []string{"Sim", "Não", "Talvez 🤔"}
	hashes := whatsmeow.HashPollOptions(options)

	for i, option := range options {
		if got, want := optionHash(option), fmt.Sprintf("%x", hashes[i]); got != want {
			t.Fatalf("hash de %q = %s, esperado %s", option, got, want)
		}
	}
}

func TestNamesForHashes(t *testing.T) {
	hashToName := optionHashMap([]string{"A", "B", "C"})

	got := namesForHashes(hashToName, []string{optionHash("C"), "desconhecido", optionHash("A")})
	if len(got) != 2 || got[0] != "C" || got[1] != "A" {
		t.Fatalf("namesForHashes = %v", got)
	}
}
