package household

import (
	"errors"
	"testing"
)

func TestProfilePINMustBeFourToSixDigits(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for _, pin := range []string{"123", "1234567", "12a4", "abcd", " 1234"} {
		if _, err := m.CreateProfile("Ada", pin); !errors.Is(err, ErrInvalidPIN) {
			t.Fatalf("CreateProfile(%q) = %v, want ErrInvalidPIN", pin, err)
		}
	}
	for _, pin := range []string{"", "1234", "123456"} {
		if _, err := m.CreateProfile("Ada", pin); err != nil {
			t.Fatalf("CreateProfile(%q) = %v, want nil", pin, err)
		}
	}
	p, err := m.CreateProfile("Bo", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateProfile(p.ID, "", "12", false); !errors.Is(err, ErrInvalidPIN) {
		t.Fatalf("UpdateProfile short pin = %v, want ErrInvalidPIN", err)
	}
	if _, err := m.UpdateProfile(p.ID, "", "4321", false); err != nil {
		t.Fatalf("UpdateProfile valid pin = %v", err)
	}
	if _, err := m.Select(p.ID, "4321"); err != nil {
		t.Fatalf("Select after valid update = %v", err)
	}
}
