package handlers

import (
	"net/http"

	"pogo.hails.cc/internal/i18n"
)

func (h *Handlers) SetLang(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	code := r.FormValue("code")
	if !i18n.Supported[code] {
		http.Redirect(w, r, r.Referer(), http.StatusSeeOther)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "lang",
		Value:    code,
		Path:     "/",
		MaxAge:   30 * 24 * 60 * 60,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})

	if u := h.currentUser(r); u != nil {
		h.db.Exec(`UPDATE users SET lang = ? WHERE id = ?`, code, u.ID)
	}

	ref := r.Referer()
	if ref == "" {
		ref = "/"
	}
	http.Redirect(w, r, ref, http.StatusSeeOther)
}
