package ui

import (
	"net/http"
)

// LoginPage is the data for the standalone sign-in page.
type LoginPage struct {
	// Next is the already-validated, same-origin path to return to.
	Next  string
	Error string
}

// RenderLogin renders the sign-in page. Verifying the token and creating
// the session happen in app/httpserver; the UI only draws the form.
func (h *Handlers) RenderLogin(w http.ResponseWriter, r *http.Request, page LoginPage, status int) {
	h.execute(w, r, "login", "login", page, status)
}
