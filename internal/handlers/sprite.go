package handlers

import (
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (h *Handlers) APITrainerSprite(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	srcURL := h.store.TrainerSpriteSourceURL(slug)
	if srcURL == "" {
		http.NotFound(w, r)
		return
	}

	if data, ok := h.store.SpriteCacheGet(slug); ok {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=2592000, immutable")
		w.Write(data)
		return
	}

	resp, err := http.Get(srcURL)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		http.NotFound(w, r)
		return
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil || len(data) == 0 {
		http.NotFound(w, r)
		return
	}

	h.store.SpriteCacheSet(slug, data)
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=2592000, immutable")
	w.Write(data)
}
