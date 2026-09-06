package household

import (
	"errors"
	"sync"
	"testing"
)

func TestProtectedProfileRateLimitsInvalidPIN(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	p, e := m.CreateProfile("Ada", "1234")
	if e != nil {
		t.Fatal(e)
	}
	for range 5 {
		if _, e = m.Select(p.ID, "bad"); e == nil {
			t.Fatal("invalid PIN was accepted")
		}
	}
	if _, e = m.Select(p.ID, "1234"); e != ErrRateLimited {
		t.Fatalf("got %v, want rate limit", e)
	}
	if _, e = m.Select(p.ID, "1234"); e != ErrRateLimited {
		t.Fatalf("locked profile accepted: %v", e)
	}
}

func TestOwnerLoginRateLimitsInvalidPassword(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Claim(m.SetupToken(), "correct password"); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if _, err := m.Login("wrong password"); !errors.Is(err, ErrCredentials) {
			t.Fatalf("wrong password = %v", err)
		}
	}
	if _, err := m.Login("correct password"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("locked owner login = %v", err)
	}
}

func TestConcurrentWrongPINAttemptsReachLockout(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	m.hashGate = make(chan struct{}, 8)
	p, err := m.CreateProfile("Ada", "1234")
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for range 5 {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, err := m.Select(p.ID, "bad"); !errors.Is(err, ErrPIN) {
				t.Errorf("wrong PIN = %v", err)
			}
		}()
	}
	group.Wait()
	if _, err := m.Select(p.ID, "1234"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("concurrent failures lost lockout: %v", err)
	}
}

func TestCredentialHashGateFailsFastWhenFull(t *testing.T) {
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Claim(m.SetupToken(), "correct password"); err != nil {
		t.Fatal(err)
	}
	m.hashGate <- struct{}{}
	m.hashGate <- struct{}{}
	defer func() { <-m.hashGate; <-m.hashGate }()
	if _, err := m.Login("correct password"); !errors.Is(err, ErrHashSaturated) {
		t.Fatalf("full hash gate = %v", err)
	}
}
