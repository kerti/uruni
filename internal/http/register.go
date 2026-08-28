package http

import (
	"net/http"

	"github.com/kerti/uruni/internal/store"
)

// registerRequest is POST /api/register's body.
type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// userResponse is the wire shape of a user row - deliberately never
// includes password_hash. store.User carries no json tags of its own
// (sqlc.yaml's emit_json_tags: false, the same reason fundResponse exists
// in setup.go), so this package owns the wire shape explicitly rather than
// relying on a field simply not being referenced.
type userResponse struct {
	ID        int64  `json:"id"`
	Email     string `json:"email"`
	CreatedAt int64  `json:"created_at"`
}

func toUserResponse(u store.User) userResponse {
	return userResponse{ID: u.ID, Email: u.Email, CreatedAt: u.CreatedAt}
}

// register is POST /api/register: the one-shot bootstrap account (ADR-030
// decision 2, issue #114). It also logs the treasurer in - RenewToken then
// Put below - because POST /setup sits *inside* the auth gate (ADR-030's
// consequences), so a register that did not establish a session would
// strand first-run setup behind a login screen nothing has offered her yet.
// The treasurer typed the password one second ago; there is no verification
// step to bridge before trusting it again.
func (a *api) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	user, err := a.auth.Register(r.Context(), req.Email, req.Password)
	if err != nil {
		mapAuthError(w, a.logger, err)
		return
	}

	// RenewToken before writing anything into the session: a token issued
	// before the identity was known to it must never be the one that ends
	// up carrying that identity (session fixation).
	if err := a.sessionManager.RenewToken(r.Context()); err != nil {
		a.logger.Error("renewing session token after register", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
		return
	}
	a.sessionManager.Put(r.Context(), sessionKeyUserID, user.ID)

	writeJSON(w, http.StatusCreated, toUserResponse(user))
}
