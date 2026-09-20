package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/config"
	"github.com/kerti/uruni/internal/db"
)

// errCreateUserUsage is returned for a wrong argument count, so the operator
// sees the one shape ADR-019's table gives the command.
var errCreateUserUsage = errors.New("usage: uruni create-user <email> <password>")

// createUser is ADR-019's `create-user <email> <password>` (#287): create the
// instance's login, or reset its password when the email already names it.
// `make dev-user` is its dev-side caller; on a deployment it is the way back
// in for a treasurer who forgot their password.
//
// Unlike seed-e2e it goes through config.Load, like `serve` and `migrate`:
// it writes the same database an operator's server reads, so it resolves
// URUNI_DB the same way and refuses an unconfigured instance the same way.
// It migrates first, so it also works on a database no server has opened.
//
// The password is never printed or logged; the email is echoed back only to
// the operator's own terminal, on stdout.
func createUser(ctx context.Context, args []string, out io.Writer) error {
	if len(args) != 2 {
		return errCreateUserUsage
	}
	email, password := args[0], args[1]

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	sqlDB, err := db.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()

	if _, err := db.Up(ctx, sqlDB, newLogger(cfg, os.Stderr)); err != nil {
		return fmt.Errorf("migrating %s: %w", cfg.DBPath, err)
	}

	created, err := auth.New(sqlDB).SetLogin(ctx, email, password)
	switch {
	case errors.Is(err, auth.ErrOtherAccountExists):
		return fmt.Errorf("%s holds one login and it is not %s - run create-user with that login's email to reset its password", cfg.DBPath, email)
	case err != nil:
		return err
	}

	if created {
		_, err = fmt.Fprintf(out, "created login %s\n", email)
	} else {
		_, err = fmt.Fprintf(out, "reset the password for %s and signed out every session\n", email)
	}
	return err
}
