package handlers

import "net/http"

type raidFinderPageData struct {
	CanCustomLobby    bool
	CanPreQueue       bool
	IncompleteProfile bool
}

func (h *Handlers) RaidFinderPage(w http.ResponseWriter, r *http.Request) {
	var d raidFinderPageData
	if u := h.currentUser(r); u != nil {
		var name, code string
		h.db.QueryRow(`SELECT COALESCE(trainer_name,''), COALESCE(trainer_code,'') FROM users WHERE id = ?`, u.ID).Scan(&name, &code)
		d.IncompleteProfile = name == "" || code == ""
		d.CanCustomLobby = h.canCustomLobby(u)
		d.CanPreQueue = h.canPreQueue(u)
	}
	h.render(w, r, "raidfinder", d)
}
