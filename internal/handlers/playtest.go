package handlers

import (
	"net/http"
	"strings"
)

// PlaytestInviteURL is the Firebase App Distribution invite link for the Android open beta
// (an invite link to the open beta tester group, made in the Firebase console). It is the
// one place the link lives: swap it here and every button on /playtest follows. Anyone
// holding it can sign up, so it is published on this page only.
const PlaytestInviteURL = "https://appdistribution.firebase.dev/i/9294d50d9722b802"

// ptCard is one small action in the guide: a picture of the screen (Img, a file in
// static/playtest, optional) and its text. Text comes from playtest.c.<ID>.title and .body.
// Warn marks the moments people get stuck on. Link adds a button (LinkKey is its label).
type ptCard struct {
	Num      int  // 1-based position in its step, filled by Playtest
	External bool // Link leaves the site (opens in a new tab), filled by Playtest
	ID       string
	Img      string
	Warn     bool
	Link     string
	LinkKey  string
	// SX, SY place the "tap here" ring on the screenshot, in percent of its width and
	// height (0 means no ring). Measured on the 432x960 images in static/playtest.
	SX, SY int
	// Miss: this screen has its own "Not seeing this screen?" text (playtest.c.<ID>.miss);
	// otherwise the generic one shows.
	Miss bool
}

// ptStep is one step of the guide. Text comes from playtest.st.<ID>.title and .intro.
// Help is the id of the help answer its "Stuck?" link opens.
type ptStep struct {
	Num   int // 1-based, filled by Playtest
	Last  bool
	ID    string
	Help  string
	Cards []ptCard
}

// playtestSteps is the walkthrough, in order, built from the Android session's recorded
// signup (Android 15, 2026-11-18) and the site's own account pages.
func playtestSteps() []ptStep {
	return []ptStep{
		{ID: "account", Help: "login", Cards: []ptCard{
			{ID: "acc_register", Img: "site-register", Link: "/register", LinkKey: "playtest.account.register", SX: 50, SY: 67},
			{ID: "acc_confirm", Img: "site-banner", SX: 50, SY: 19},
			{ID: "acc_profile", Img: "site-settings", SX: 45, SY: 32},
			{ID: "acc_location", Img: "site-location", SX: 50, SY: 61},
			{ID: "acc_two", Warn: true},
			{ID: "acc_signin", Img: "site-login", SX: 50, SY: 82},
		}},
		{ID: "join", Help: "email", Cards: []ptCard{
			{ID: "join_open", Img: "03-google-signin", Link: PlaytestInviteURL, LinkKey: "playtest.join_button", SX: 50, SY: 40},
			{ID: "join_signup", Img: "a-ready-to-test", Warn: true, SX: 50, SY: 67, Miss: true},
			{ID: "join_in", Img: "b-youre-in", SX: 50, SY: 54},
			{ID: "join_accept", Img: "c-test-apps-pending", SX: 32, SY: 37, Miss: true},
			{ID: "join_consent", Img: "d-consent", Warn: true, SX: 12, SY: 66, Miss: true},
			{ID: "join_release", Img: "e-release-page", SX: 65, SY: 94},
		}},
		{ID: "tester", Help: "signin", Cards: []ptCard{
			{ID: "t_intro"},
			{ID: "t_download", Img: "f-download-apk", SX: 80, SY: 64, Miss: true},
			{ID: "t_harmful", Img: "g-file-might-be-harmful", SX: 77, SY: 57, Miss: true},
			{ID: "t_open", Img: "h-downloaded-open", SX: 85, SY: 15, Miss: true},
			{ID: "t_blocked", Img: "j-unknown-apps-blocked", SX: 85, SY: 57, Miss: true},
			{ID: "t_allow", Img: "k-allow-from-source", Warn: true, SX: 88, SY: 43},
			{ID: "t_install", Img: "l-install-app-tester", SX: 82, SY: 57},
			{ID: "t_done", Img: "m-app-tester-installed", SX: 82, SY: 57},
		}},
		{ID: "signin", Help: "missing", Cards: []ptCard{
			{ID: "s_google", Img: "n-app-tester-signin", SX: 55, SY: 57},
			{ID: "s_choose", Img: "p-choose-account", Warn: true, SX: 50, SY: 53, Miss: true},
			{ID: "s_share", Img: "o-app-tester-share-info", SX: 63, SY: 84},
			{ID: "s_access", Img: "q-access-checkbox", Warn: true, SX: 85, SY: 58, Miss: true},
			{ID: "s_list", Img: "s-app-tester-list", SX: 30, SY: 24, Miss: true},
			{ID: "s_start", Img: "t-start-testing-consent", SX: 11, SY: 52},
		}},
		{ID: "install", Help: "notinstalled", Cards: []ptCard{
			{ID: "i_download", Img: "u-flexdex-release", SX: 18, SY: 53},
			{ID: "i_notif", Img: "v-app-tester-notifications", SX: 50, SY: 54},
			{ID: "i_wait", Img: "w-downloading", SX: 25, SY: 55},
			{ID: "i_unknown", Img: "y2-app-tester-allow", Warn: true, SX: 88, SY: 44, Miss: true},
			{ID: "i_install", Img: "y4-install-flexdex", SX: 82, SY: 55, Miss: true},
			{ID: "i_open", Img: "y5-installed-open", SX: 18, SY: 53},
		}},
		{ID: "first", Help: "bubble", Cards: []ptCard{
			{ID: "f_notif", Img: "y6-flexdex-notifications", SX: 50, SY: 53},
			{ID: "f_login", Img: "y7-flexdex-login", SX: 50, SY: 60, Miss: true},
			{ID: "f_perms"},
		}},
	}
}

// Playtest is the Android open beta guide. Public on purpose: a stranger has to read it
// before they have an account.
func (h *Handlers) Playtest(w http.ResponseWriter, r *http.Request) {
	steps := playtestSteps()
	for i := range steps {
		steps[i].Num, steps[i].Last = i+1, i == len(steps)-1
		for j := range steps[i].Cards {
			c := &steps[i].Cards[j]
			c.Num, c.External = j+1, c.Link != "" && !strings.HasPrefix(c.Link, "/")
		}
	}
	build, sha, scanned := h.mobileBuildScan()
	h.render(w, r, "playtest", struct {
		InviteURL string
		Steps     []ptStep
		Total     int
		Build     int
		SHA256    string
		Scanned   bool
	}{PlaytestInviteURL, steps, len(steps), build, sha, scanned})
}
