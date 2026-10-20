package auth

import (
	"context"
	"errors"
	"testing"
)

func TestCommonPasswordListIsLoadedAndNonTrivial(t *testing.T) {
	t.Parallel()
	isCommonPassword("x") // force the load
	if n := len(commonPasswords); n < 1500 {
		t.Errorf("common password list has %d entries, want at least 1500", n)
	}
	for k := range commonPasswords {
		if len(k) < MinPasswordLength {
			t.Fatalf("entry %q is shorter than MinPasswordLength; the filter should have dropped it", k)
		}
	}
}

func TestIsCommonPasswordIgnoresCase(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"password", "PASSWORD", "PassWord", "12345678", "qwertyuiop"} {
		if !isCommonPassword(p) {
			t.Errorf("isCommonPassword(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"correct-horse-battery", "zk3-vq9-lunar-tide"} {
		if isCommonPassword(p) {
			t.Errorf("isCommonPassword(%q) = true, want false", p)
		}
	}
}

func TestRegisterAndSetLoginRefuseACommonPassword(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	for _, p := range []string{"password", "PASSWORD", "Qwertyuiop"} {
		a, _ := newTestAuth(t)
		if _, err := a.Register(ctx, "treasurer@example.org", p); !errors.Is(err, ErrPasswordTooCommon) || !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("Register(%q) = %v, want ErrPasswordTooCommon (an ErrInvalidArgument)", p, err)
		}
		if _, err := a.SetLogin(ctx, "treasurer@example.org", p); !errors.Is(err, ErrPasswordTooCommon) {
			t.Errorf("SetLogin(%q) = %v, want ErrPasswordTooCommon", p, err)
		}
	}

	a, _ := newTestAuth(t)
	if _, err := a.Register(ctx, "treasurer@example.org", "zk3-vq9-lunar-tide"); err != nil {
		t.Errorf("Register(unlisted) = %v, want nil", err)
	}
}
