package auth

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHashPasswordUsesArgon2idPHC(t *testing.T) {
	encoded, err := HashPassword(context.Background(), "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("phc = %s", encoded)
	}
	ok, err := VerifyPassword(context.Background(), encoded, "correct horse battery")
	if err != nil || !ok {
		t.Fatalf("verify = %v %v", ok, err)
	}
	ok, err = VerifyPassword(context.Background(), encoded, "wrong password!!")
	if err != nil || ok {
		t.Fatalf("wrong password verify = %v %v", ok, err)
	}
}

func TestArgonSemaphoreReturnsBusy(t *testing.T) {
	release := make(chan struct{})
	var ready sync.WaitGroup
	var done sync.WaitGroup
	hold := func() {
		defer done.Done()
		free, err := acquireArgon(context.Background())
		if err != nil {
			t.Errorf("hold: %v", err)
			ready.Done()
			return
		}
		ready.Done()
		<-release
		free()
	}
	ready.Add(2)
	done.Add(2)
	go hold()
	go hold()
	ready.Wait()
	start := time.Now()
	_, err := acquireArgon(context.Background())
	if err != ErrBusy {
		close(release)
		t.Fatalf("third acquire = %v, want busy", err)
	}
	close(release)
	done.Wait()
	if time.Since(start) < 1500*time.Millisecond {
		t.Fatalf("busy returned in %s, want about 2s", time.Since(start))
	}
}
