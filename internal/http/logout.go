package http

import "net/http"

// logout is POST /api/logout (#116). One of the two routes that never answer
// 401 (see sessionRequired for why it and getSession stay outside the gate).
//
// Destroy removes the session row and marks the session Destroyed in context,
// so LoadAndSave (api.go) writes back an already-expired cookie, not the
// refreshed one; the browser drops it on receipt.
//
// 204 unconditionally, even with no session to destroy (an expired or
// never-issued cookie): that caller is asking for what logout gives, "make sure
// I am logged out", and a 401 would be hostile for no gain. Exists is checked
// first only to skip a pointless store round trip.
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
