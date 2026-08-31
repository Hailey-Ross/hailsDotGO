package handlers

import (
	"net/http"
	"strings"
)

// boxProvenance records how a user_pokemon_box row came to exist.
//
// The box used to be private, so nothing needed to know: a wrong entry only ever
// misled the person who typed it. A profile will display dex completion from it,
// and once a collection has an audience the display has to be able to answer
// "how do you know this?" for every row it counts.
//
// The goal is NOT to keep false entries out. Manual entry exists and is going to
// keep existing, so unverified rows are unavoidable. The goal is narrower and
// actually achievable: never present an unverified row as though it were
// verified.
type boxProvenance string

const (
	// provManual is a spread a human typed into the calculator or the box page.
	provManual boxProvenance = "manual"

	// provScanWeb is a screenshot uploaded through the browser.
	provScanWeb boxProvenance = "scan_web"

	// provScanApp is a reading submitted by the mobile app.
	provScanApp boxProvenance = "scan_app"

	// provScanAppAttested is provScanApp from a build that passed integrity
	// attestation. Nothing sets this yet; the attestation check is mobile work.
	provScanAppAttested boxProvenance = "scan_app_attested"

	// provUnknown is a row whose origin was never recorded.
	//
	// Every row written before the provenance column existed is this, and it is
	// deliberately not provManual: "we do not know" and "a human typed it" are
	// different claims and only the first is true of them. It is the zero value
	// for the same reason, so a caller that forgets to supply a claim degrades
	// to the least trusted value rather than the most convenient one.
	provUnknown boxProvenance = "unknown"
)

// isMobileAPI reports whether a request arrived on the versioned mobile API
// rather than the site's own endpoints.
//
// The two share their handlers, so the path is the only thing distinguishing a
// write from the app from a write from the browser. chi matches on a copy held
// in the route context and never rewrites r.URL.Path, so this stays the full
// request path inside a mounted subrouter.
func isMobileAPI(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/api/mobile/v1/")
}

// resolveProvenance decides what to store for a write.
//
// The client says only whether the values came off a scan or out of a keyboard.
// Everything that carries trust is decided here, from facts the client cannot
// forge: which route the request arrived on, and whether an attestation token
// actually verified. A client that asks to be recorded as attested is answered
// with whatever its route and token really justify, which for an unattested app
// build is provScanApp and for a browser is provScanWeb.
//
// That split is the whole point of the column. A provenance value copied out of
// the request body would say exactly what the least honest client wanted it to
// say, which is worth less than not recording it at all: it would let a forged
// row outrank an honest one.
//
// An unrecognised or absent claim is provUnknown rather than an error. Older app
// builds are on testers' phones and send nothing here, and refusing their writes
// would break saving a Pokemon to punish them for a field they predate.
func resolveProvenance(r *http.Request, claim string, attested bool) boxProvenance {
	switch strings.ToLower(strings.TrimSpace(claim)) {
	case "manual":
		return provManual
	case "scan":
		if !isMobileAPI(r) {
			return provScanWeb
		}
		if attested {
			return provScanAppAttested
		}
		return provScanApp
	default:
		return provUnknown
	}
}

// verified reports whether the row's origin is a machine reading of the game's
// own interface, as opposed to a human's typing or an unrecorded origin.
//
// Kept next to the values rather than written out at each call site, so a new
// provenance value has one place to be classified.
func (p boxProvenance) verified() bool {
	return p == provScanApp || p == provScanAppAttested || p == provScanWeb
}
