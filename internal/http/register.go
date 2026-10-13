package http

import (
	"errors"
	"net/http"

	"github.com/kerti/uruni/internal/auth"
	"github.com/kerti/uruni/internal/store"
)

// registerRequest is POST /api/register's body.
type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// userResponse is the wire shape of a user row, never including
// password_hash. store.User has no json tags (sqlc.yaml emit_json_tags: false,
// as for fundResponse in setup.go), so this package owns the wire shape
// instead of relying on a field going unreferenced.
type userResponse struct {
	ID        int64  `json:"id"`
	Email     string `json:"email"`
	CreatedAt int64  `json:"created_at"`
}

func toUserResponse(u store.User) userResponse {
	return userResponse{ID: u.ID, Email: u.Email, CreatedAt: u.CreatedAt}
}

// register is POST /api/register: the one-shot bootstrap account (ADR-030
// decision 2, #114). It also logs the treasurer in (RenewToken then Put)
// because POST /setup sits inside the auth gate (ADR-030), so a register
// without a session would strand first-run setup behind a login screen
// nothing has offered her. She typed the password a second ago; nothing needs
// verifying again.
//
// Register is public, so bounded as login is (#495): rate limited per client
// IP, the attempt reserved before auth.Register runs so a burst is counted in
// flight. Auth.Register refuses an instance that already has an account
// before hashing, and every hash goes through the process-wide gate. The
// reservation stands whenever Register judged the attempt (created, invalid,
// already registered) and is handed back when it did not (busy, broken).
// Success keeps the count too: unlike login there is no reset.
func (a *api) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	reservation, ok := a.registerLimiter.reserve("register-ip:" + a.clientNetwork(r))
	if !ok {
		writeAPIError(w, http.StatusTooManyRequests, "too_many_requests", "Too many attempts. Try again later.")
		return
	}

	user, err := a.auth.Register(r.Context(), req.Email, req.Password)
	if err != nil {
		if !errors.Is(err, auth.ErrInvalidArgument) && !errors.Is(err, auth.ErrAlreadyRegistered) {
			reservation.release()
		}
		mapAuthError(w, a.logger, err)
		return
	}

	// RenewToken before writing anything into the session: a token issued before
	// the identity was known must never carry that identity (session fixation).
	if err := a.sessionManager.RenewToken(r.Context()); err != nil {
		a.logger.Error("renewing session token after register", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}
	a.sessionManager.Put(r.Context(), sessionKeyUserID, user.ID)

	writeJSON(w, http.StatusCreated, toUserResponse(user))
}
