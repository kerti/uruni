package http

import "net/http"

// logout is POST /api/logout (#116). One of the two routes that never
// answer 401 - see sessionRequired's own comment for why it and getSession
// stay outside the gate.
//
// Destroy removes the session row from the store and marks the session
// Destroyed in context, which is what makes LoadAndSave (api.go) write back
// an already-expired cookie rather than the ordinary refreshed one - the
// browser drops it on receipt, so there is nothing left to send on the next
// request either.
//
// 204 unconditionally, including when there was no session to destroy (an
// already-expired or never-issued cookie): a caller in that state is asking
// for exactly what logout gives - "make sure I am logged out" - and
// refusing that with a 401 would be hostile for no gain. Exists is checked
// first only to skip a pointless store round trip, not to change the
// response.
func (a *api) logout(w http.ResponseWriter, r *http.Request) {
	if a.sessionManager.Exists(r.Context(), sessionKeyUserID) {
		if err := a.sessionManager.Destroy(r.Context()); err != nil {
			a.logger.Error("destroying session on logout", "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "Something went wrong.")
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
