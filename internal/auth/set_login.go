package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kerti/uruni/internal/store"
)

// SetLogin is the `create-user` command's whole job (ADR-019, #287): create
// the instance's login, or reset its password when email already names it.
// It reports created=true for a new row and false for a reset.
//
// It keeps Register's one-login invariant (ADR-030 decision 2) rather than
// sidestepping it just because it runs from the CLI instead of over HTTP:
// with a login already present under another email it returns
// ErrOtherAccountExists and writes nothing. A second row would get funds[0]
// from resolveFund exactly as a second registration would.
//
// A reset also deletes every session, so a cookie issued under the old
// password stops working the moment the password changes.
func (a *Auth) SetLogin(ctx context.Context, email, password string) (created bool, err error) {
	email = strings.TrimSpace(email)
	if email == "" || !strings.Contains(email, "@") {
		return false, fmt.Errorf("%w: email must be a valid address", ErrInvalidArgument)
	}
	if len(password) < MinPasswordLength {
		return false, fmt.Errorf("%w: password must be at least %d characters", ErrInvalidArgument, MinPasswordLength)
	}

	// Hashed outside the transaction, for the reason Register gives.
	hash, err := hashPassword(password)
	if err != nil {
		return false, fmt.Errorf("hashing password: %w", err)
	}

	err = a.withTx(ctx, func(q store.Querier) error {
		_, err := q.UpdateUserPassword(ctx, store.UpdateUserPasswordParams{PasswordHash: hash, Email: email})
		switch {
		case err == nil:
			if err := q.DeleteAllSessions(ctx); err != nil {
				return fmt.Errorf("signing out existing sessions: %w", err)
			}
			return nil
		case !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("resetting password: %w", err)
		}

		count, err := q.CountUsers(ctx)
		if err != nil {
			return fmt.Errorf("checking for an existing account: %w", err)
		}
		if count > 0 {
			return ErrOtherAccountExists
		}

		if _, err := q.CreateUser(ctx, store.CreateUserParams{
			Email:        email,
			PasswordHash: hash,
			CreatedAt:    time.Now().Unix(),
		}); err != nil {
			return fmt.Errorf("creating user: %w", err)
		}
		created = true
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("setting login: %w", err)
	}
	return created, nil
}
