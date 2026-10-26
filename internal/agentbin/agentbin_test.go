package agentbin

import (
	"bytes"
	"errors"
	"os"
	"slices"
	"testing"
)

// Release and Nix builds set TAILSUITE_REQUIRE_AGENT=1, so their tests
// fail unless both agents are embedded, each a Linux ELF binary for its
// architecture.
func TestEmbedded(t *testing.T) {
	got := Embedded()
	if os.Getenv("TAILSUITE_REQUIRE_AGENT") == "1" && !slices.Equal(got, Arches) {
		t.Fatalf("embedded for %v, want %v", got, Arches)
	}
	machine := map[string]byte{"amd64": 0x3e, "arm64": 0xb7} // ELF e_machine
	for _, a := range got {
		b, err := For(a)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(b, []byte("\x7fELF")) || len(b) < 20 || b[18] != machine[a] {
			t.Errorf("%s: not a Linux ELF binary for %s", Name(a), a)
		}
	}
}

func TestForAnUnknownArch(t *testing.T) {
	if _, err := For("riscv64"); err == nil {
		t.Fatal("got an agent for riscv64")
	}
	if len(Embedded()) == 0 {
		if _, err := For("amd64"); !errors.Is(err, ErrNotEmbedded) {
			t.Errorf("got %v", err)
		}
	}
}
