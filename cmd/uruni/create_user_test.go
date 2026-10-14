package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/db"
)

// TestRunDispatchesCreateUser: `make dev-user` invokes this name (ADR-019); #287
// was exactly this wiring missing.
func TestRunDispatchesCreateUser(t *testing.T) {
	t.Setenv("URUNI_DB", filepath.Join(t.TempDir(), "uruni.db"))
	t.Setenv("URUNI_BASE_URL", "https://uruni.test")
	t.Setenv("URUNI_BACKUP_DIR", t.TempDir())

	if err := run([]string{"create-user", "treasurer@example.org", "correct-horse-battery"}); err != nil {
		t.Fatalf("run([create-user ...]) = %v, want nil", err)
	}
}

// TestCreateUserCreatesThenResets is the issue's acceptance pair: the first
// run creates the login on a fresh database, the second resets it instead of
// failing - and the password appears in neither line of output.
func TestCreateUserCreatesThenResets(t *testing.T) {
	t.Setenv("URUNI_DB", filepath.Join(t.TempDir(), "uruni.db"))
	t.Setenv("URUNI_BASE_URL", "https://uruni.test")
	t.Setenv("URUNI_BACKUP_DIR", t.TempDir())
	ctx := context.Background()

	var first, second bytes.Buffer
	if err := createUser(ctx, []string{"treasurer@example.org", "correct-horse-battery"}, strings.NewReader(""), &first, io.Discard); err != nil {
		t.Fatalf("first createUser() = %v, want nil", err)
	}
	if err := createUser(ctx, []string{"treasurer@example.org", "another-long-enough-password"}, strings.NewReader(""), &second, io.Discard); err != nil {
		t.Fatalf("second createUser() = %v, want nil", err)
	}

	if !strings.HasPrefix(first.String(), "created") {
		t.Errorf("first output = %q, want it to report a created login", first.String())
	}
	if !strings.HasPrefix(second.String(), "reset") {
		t.Errorf("second output = %q, want it to report a reset", second.String())
	}
	for _, out := range []string{first.String(), second.String()} {
		if strings.Contains(out, "correct-horse-battery") || strings.Contains(out, "another-long-enough-password") {
			t.Errorf("output %q contains the password", out)
		}
	}
}

// TestCreateUserNormalisesTheEmail (#500): a capitalised or padded address
// creates the lower-case login, a differently-cased one resets it rather than
// being refused as another account, and the output names the stored form.
func TestCreateUserNormalisesTheEmail(t *testing.T) {
	t.Setenv("URUNI_DB", filepath.Join(t.TempDir(), "uruni.db"))
	t.Setenv("URUNI_BASE_URL", "https://uruni.test")
	t.Setenv("URUNI_BACKUP_DIR", t.TempDir())
	ctx := context.Background()

	var first, second bytes.Buffer
	if err := createUser(ctx, []string{" Treasurer@Example.ORG ", "correct-horse-battery"}, strings.NewReader(""), &first, io.Discard); err != nil {
		t.Fatalf("first createUser() = %v, want nil", err)
	}
	if err := createUser(ctx, []string{"TREASURER@example.org", "another-long-enough-password"}, strings.NewReader(""), &second, io.Discard); err != nil {
		t.Fatalf("second createUser() = %v, want a reset", err)
	}
	if want := "created login treasurer@example.org\n"; first.String() != want {
		t.Errorf("first output = %q, want %q", first.String(), want)
	}
	if !strings.HasPrefix(second.String(), "reset the password for treasurer@example.org") {
		t.Errorf("second output = %q, want a reset of treasurer@example.org", second.String())
	}
}

func TestCreateUserRefusesASecondLogin(t *testing.T) {
	t.Setenv("URUNI_DB", filepath.Join(t.TempDir(), "uruni.db"))
	t.Setenv("URUNI_BASE_URL", "https://uruni.test")
	t.Setenv("URUNI_BACKUP_DIR", t.TempDir())
	ctx := context.Background()

	var out bytes.Buffer
	if err := createUser(ctx, []string{"first@example.org", "correct-horse-battery"}, strings.NewReader(""), &out, io.Discard); err != nil {
		t.Fatalf("first createUser() = %v, want nil", err)
	}
	err := createUser(ctx, []string{"second@example.org", "another-long-enough-password"}, strings.NewReader(""), &out, io.Discard)
	if err == nil || strings.Contains(err.Error(), "another-long-enough-password") {
		t.Fatalf("createUser(second email) = %v, want a refusal that does not echo the password", err)
	}
}

func TestCreateUserWantsOneOrTwoArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"a@b", "c", "d"}} {
		if err := createUser(context.Background(), args, strings.NewReader(""), &bytes.Buffer{}, io.Discard); !errors.Is(err, errCreateUserUsage) {
			t.Errorf("createUser(%q) = %v, want errCreateUserUsage", args, err)
		}
	}
}

// TestCreateUserReadsThePasswordFromStdin (#522): with only the email, the
// password is the first line of stdin, line ending dropped and spaces kept, so
// it never has to appear in shell history or ps. No warning is printed.
func TestCreateUserReadsThePasswordFromStdin(t *testing.T) {
	t.Setenv("URUNI_DB", filepath.Join(t.TempDir(), "uruni.db"))
	t.Setenv("URUNI_BASE_URL", "https://uruni.test")
	t.Setenv("URUNI_BACKUP_DIR", t.TempDir())
	ctx := context.Background()

	var out, errOut bytes.Buffer
	in := strings.NewReader(" correct horse battery \r\nignored second line\n")
	if err := createUser(ctx, []string{"treasurer@example.org"}, in, &out, &errOut); err != nil {
		t.Fatalf("createUser(stdin) = %v, want nil", err)
	}
	if want := "created login treasurer@example.org\n"; out.String() != want {
		t.Errorf("output = %q, want %q", out.String(), want)
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr = %q, want nothing when the password came on stdin", errOut.String())
	}

	// The stored password is the line with its spaces, not a trimmed one.
	sqlDB, err := db.Open(ctx, os.Getenv("URUNI_DB"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	if _, err := auth.New(sqlDB).Authenticate(ctx, "treasurer@example.org", " correct horse battery "); err != nil {
		t.Errorf("Authenticate(stdin password) = %v, want nil", err)
	}
}

// TestCreateUserRefusesAnEmptyStdin: an empty pipe or a blank line is an
// operator mistake, never an empty password.
func TestCreateUserRefusesAnEmptyStdin(t *testing.T) {
	t.Setenv("URUNI_DB", filepath.Join(t.TempDir(), "uruni.db"))
	t.Setenv("URUNI_BASE_URL", "https://uruni.test")
	t.Setenv("URUNI_BACKUP_DIR", t.TempDir())

	for _, in := range []string{"", "\n", "\r\n"} {
		err := createUser(context.Background(), []string{"treasurer@example.org"}, strings.NewReader(in), io.Discard, io.Discard)
		if !errors.Is(err, errNoPassword) {
			t.Errorf("createUser(stdin %q) = %v, want errNoPassword", in, err)
		}
	}
}

// TestCreateUserWarnsAboutAPasswordArgument (#522): the argument form still
// works, but says on stderr where the password just went - without repeating it.
func TestCreateUserWarnsAboutAPasswordArgument(t *testing.T) {
	t.Setenv("URUNI_DB", filepath.Join(t.TempDir(), "uruni.db"))
	t.Setenv("URUNI_BASE_URL", "https://uruni.test")
	t.Setenv("URUNI_BACKUP_DIR", t.TempDir())

	var out, errOut bytes.Buffer
	if err := createUser(context.Background(), []string{"treasurer@example.org", "correct-horse-battery"}, strings.NewReader(""), &out, &errOut); err != nil {
		t.Fatalf("createUser(argument) = %v, want nil", err)
	}
	if errOut.String() != warnPasswordArgument {
		t.Errorf("stderr = %q, want the argument warning", errOut.String())
	}
	if strings.Contains(errOut.String(), "correct-horse-battery") {
		t.Errorf("warning %q repeats the password", errOut.String())
	}
}
