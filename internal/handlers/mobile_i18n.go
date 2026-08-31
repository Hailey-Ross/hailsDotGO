package handlers

// The site's string bundles, for the mobile app.
//
// Every screen the app takes native is a screen whose strings stop coming from this project's
// translation workflow and start coming from Android string resources. Translators approve a
// string, it goes live on the website, and the app carries on showing whatever was compiled into
// its last release. That is the difference between "the app is native" and "the app is native in
// English".
//
// Serving the bundles closes it: an approved translation reaches devices on their next launch,
// with no app release, exactly as it reaches the website with no deploy.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"net/http"

	"github.com/go-chi/chi/v5"
	"pogo.hails.cc/internal/i18n"
)

// MobileLangs lists the locales the site currently offers.
//
// h.publicLangs, not i18n.Langs: the first is what the site's own switcher shows and what SetLang
// will accept, the second is everything compiled in or registered, including locales an admin has
// switched off. An app whose picker offered a disabled locale would let a trainer choose one the
// site would refuse to store.
func (h *Handlers) MobileLangs(w http.ResponseWriter, r *http.Request) {
	writeJSONWithETag(w, r, map[string]any{"languages": h.publicLangs()})
}

// MobileI18nBundle serves one locale as a flat {key: value} map.
//
// The bundle is RESOLVED, not raw. i18n.Bundle(lang) is the locale's own file merged with its
// approved overlay and nothing else, which for every locale but English is a partial map: en.json
// carries 1345 keys and de.json 777, because a translation that has not been done yet is an absent
// key rather than an empty one. The website never notices, since TFunc falls back to English per
// lookup. A client handed the raw bundle would render a raw key like "nav.raids" in the middle of
// its navigation.
//
// So English is laid down first and the locale merged over it, which reproduces TFunc's precedence
// exactly: overlay, then the locale, then English. Do not "simplify" this to i18n.Bundle(lang).
func (h *Handlers) MobileI18nBundle(w http.ResponseWriter, r *http.Request) {
	lang := chi.URLParam(r, "lang")
	if !h.langEnabled(lang) {
		// Enabled, not merely supported. A locale an admin has switched off is not
		// one the site is willing to serve, and answering with it anyway would let
		// the app render in a language the website will not.
		writeJSONError(w, "unknown language", http.StatusNotFound)
		return
	}

	merged := i18n.Bundle("en")
	if lang != "en" {
		maps.Copy(merged, i18n.Bundle(lang))
	}
	writeJSONWithETag(w, r, merged)
}

// writeJSONWithETag marshals v, tags it with a hash of its own bytes, and answers 304 when the
// caller already has that exact body.
//
// A content hash rather than a version or a timestamp because there is nothing else honest to
// hang it on: an approved translation changes an overlay file on disk with no counter attached,
// and i18n.OverlayCount is not enough on its own, since an edit that REPLACES a value leaves the
// count where it was.
//
// no-cache rather than no-store: the client should keep the body and revalidate. These payloads
// are 50 to 80 KB, so the difference between revalidating and refetching is the difference
// between a few hundred bytes and the whole bundle on every launch.
func writeJSONWithETag(w http.ResponseWriter, r *http.Request, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		writeJSONError(w, "server error", http.StatusInternalServerError)
		return
	}
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:])[:16] + `"`

	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}
