package handlers

import (
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// Bounded, like every other outbound client in the repo. http.Get uses
// http.DefaultClient, which has no timeout at all, so a slow origin would pin a
// request goroutine here indefinitely.
var trainerSpriteHTTP = &http.Client{Timeout: 20 * time.Second}

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

	resp, err := trainerSpriteHTTP.Get(srcURL)
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
