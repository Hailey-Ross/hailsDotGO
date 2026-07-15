package costumes

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// maxSprite caps what we will read from the origin. The 256x256 icons run ~20 KB; the trainer
// proxy's 64 KB limit is too tight for them.
const maxSprite = 256 << 10

var (
	spriteHTTP = &http.Client{Timeout: 20 * time.Second}

	spriteMu    sync.RWMutex
	spriteCache = map[string][]byte{}
)

// SpriteProxy serves a shiny costume sprite from our own origin.
//
// We deliberately do not hotlink: every costumed row in the shiny checklist would otherwise
// push a request onto someone else's host from the user's browser. Instead a sprite is pulled
// from the mined-asset CDN exactly once, then served from disk forever after.
//
// The response is immutable because the origin URL is pinned to an asset commit, so both the
// browser and Caddy can hold it indefinitely.
func SpriteProxy(cacheDir string) http.HandlerFunc {
	dir := filepath.Join(cacheDir, "costumes")
	os.MkdirAll(dir, 0o755)

	return func(w http.ResponseWriter, r *http.Request) {
		name := filepath.Base(chi.URLParam(r, "file"))

		// Only filenames the catalog can actually produce. Without this gate the endpoint would
		// fetch attacker-chosen paths from the origin on demand, i.e. an open proxy.
		if !AllowedFile(name) {
			http.NotFound(w, r)
			return
		}

		if data, ok := spriteFromCache(dir, name); ok {
			writeSprite(w, data)
			return
		}

		resp, err := spriteHTTP.Get(OriginURL(name))
		if err != nil {
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			http.NotFound(w, r)
			return
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxSprite))
		if err != nil || len(data) == 0 {
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
			return
		}

		os.WriteFile(filepath.Join(dir, name), data, 0o644)
		spriteMu.Lock()
		spriteCache[name] = data
		spriteMu.Unlock()

		writeSprite(w, data)
	}
}

// spriteFromCache checks memory, then disk, promoting a disk hit into memory. Disk is the
// durable tier: cache/ survives deploys, so a redeploy does not re-fetch every sprite.
func spriteFromCache(dir, name string) ([]byte, bool) {
	spriteMu.RLock()
	data, ok := spriteCache[name]
	spriteMu.RUnlock()
	if ok {
		return data, true
	}

	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil || len(data) == 0 {
		return nil, false
	}
	spriteMu.Lock()
	spriteCache[name] = data
	spriteMu.Unlock()
	return data, true
}

func writeSprite(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=2592000, immutable")
	w.Write(data)
}
