package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/kerti/uruni/internal/store"
)

// ChangeEmail and ChangePassword are the treasurer editing her own login from
// Pengaturan (#386). The instance still has one account (ADR-030 decision 2);
// these edit that row and add no second one.
//
// Both take the CURRENT password and check it through the same gate as a login
// (Auth.verify), so a burst of changes can't out-spend the hashing ceiling, and
// answer ErrWrongCurrentPassword when it fails. ErrBusy means nothing was
// checked, as for Authenticate, and the caller must not count it as a guess.
// The shape checks (the new email, the new password) run first and need no
// hashing: a malformed request is refused before it costs a computation, and
// is no guess at the password either.
//
// Either change deletes every session row but keepSession (the token of the
// one making the change; "" keeps none), in the same transaction as the update.
// That alone does not end a session: a request that loaded its session before
// the transaction commits it back afterwards (scs writes every loaded session
// back, session_store.go), so the caller also binds sessions to the credential
// (internal/http credentialFingerprint) and the returned row is what it
// fingerprints.

// ChangeEmail replaces the login's address with newEmail, normalised
// (NormalizeEmail) and validated as Register does, and returns the updated row.
func (a *Auth) ChangeEmail(ctx context.Context, userID int64, currentPassword, newEmail, keepSession string) (store.User, error) {
	email := NormalizeEmail(newEmail)
	if err := checkEmail(email); err != nil {
		return store.User{}, err
	}
	if err := a.verifyCurrent(ctx, userID, currentPassword); err != nil {
		return store.User{}, err
	}

	var user store.User
	err := a.withTx(ctx, func(q store.Querier) error {
		var err error
		user, err = q.UpdateUserEmail(ctx, store.UpdateUserEmailParams{Email: email, ID: userID})
		if err != nil {
			return fmt.Errorf("updating email: %w", err)
		}
		if err := q.DeleteOtherSessions(ctx, keepSession); err != nil {
			return fmt.Errorf("signing out other sessions: %w", err)
		}
		return nil
	})
	if err != nil {
		return store.User{}, fmt.Errorf("changing email: %w", err)
	}
	return user, nil
}

// ChangePassword replaces the login's password with newPassword, held to
// MinPasswordLength as Register holds it and hashed through the same gate. It
// returns the updated row, whose new password_hash is the credential.
func (a *Auth) ChangePassword(ctx context.Context, userID int64, currentPassword, newPassword, keepSession string) (store.User, error) {
	if err := checkPassword("new_password", newPassword); err != nil {
		return store.User{}, err
	}
	if err := a.verifyCurrent(ctx, userID, currentPassword); err != nil {
		return store.User{}, err
	}

	// Hashed outside the transaction, for the reason Register gives.
	hash, err := a.hash(ctx, newPassword)
	if err != nil {
		return store.User{}, fmt.Errorf("hashing password: %w", err)
	}

	var user store.User
	err = a.withTx(ctx, func(q store.Querier) error {
		var err error
		user, err = q.UpdateUserPasswordByID(ctx, store.UpdateUserPasswordByIDParams{PasswordHash: hash, ID: userID})
		if err != nil {
			return fmt.Errorf("updating password: %w", err)
		}
		if err := q.DeleteOtherSessions(ctx, keepSession); err != nil {
			return fmt.Errorf("signing out other sessions: %w", err)
		}
		return nil
	})
	if err != nil {
		return store.User{}, fmt.Errorf("changing password: %w", err)
	}
	return user, nil
}

// verifyCurrent checks password against the stored hash of userID's row.
func (a *Auth) verifyCurrent(ctx context.Context, userID int64, password string) error {
	user, err := a.q.GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// A session naming a row that is gone: nothing a request can fix.
			return fmt.Errorf("session names no account (id %d): %w", userID, err)
		}
		return fmt.Errorf("looking up account: %w", err)
	}
	ok, err := a.verify(ctx, password, user.PasswordHash)
	if err != nil {
		return fmt.Errorf("verifying password: %w", err)
	}
	if !ok {
		return ErrWrongCurrentPassword
	}
	return nil
}
