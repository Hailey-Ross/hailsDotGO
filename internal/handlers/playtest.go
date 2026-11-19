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
			{ID: "acc_register", Img: "site-register", Link: "/register", LinkKey: "playtest.account.register"},
			{ID: "acc_confirm", Img: "site-banner"},
			{ID: "acc_profile", Img: "site-settings"},
			{ID: "acc_location", Img: "site-location"},
			{ID: "acc_two", Warn: true},
			{ID: "acc_signin", Img: "site-login"},
		}},
		{ID: "join", Help: "email", Cards: []ptCard{
			{ID: "join_open", Img: "03-google-signin", Link: PlaytestInviteURL, LinkKey: "playtest.join_button"},
			{ID: "join_signup", Img: "a-ready-to-test", Warn: true},
			{ID: "join_in", Img: "b-youre-in"},
			{ID: "join_accept", Img: "c-test-apps-pending"},
			{ID: "join_consent", Img: "d-consent", Warn: true},
			{ID: "join_release", Img: "e-release-page"},
		}},
		{ID: "tester", Help: "signin", Cards: []ptCard{
			{ID: "t_intro"},
			{ID: "t_download", Img: "f-download-apk"},
			{ID: "t_harmful", Img: "g-file-might-be-harmful"},
			{ID: "t_open", Img: "h-downloaded-open"},
			{ID: "t_blocked", Img: "j-unknown-apps-blocked"},
			{ID: "t_allow", Img: "k-allow-from-source", Warn: true},
			{ID: "t_install", Img: "l-install-app-tester"},
			{ID: "t_done", Img: "m-app-tester-installed"},
		}},
		{ID: "signin", Help: "missing", Cards: []ptCard{
			{ID: "s_google", Img: "n-app-tester-signin"},
			{ID: "s_choose", Img: "p-choose-account", Warn: true},
			{ID: "s_share", Img: "o-app-tester-share-info"},
			{ID: "s_access", Img: "q-access-checkbox", Warn: true},
			{ID: "s_list", Img: "s-app-tester-list"},
			{ID: "s_start", Img: "t-start-testing-consent"},
		}},
		{ID: "install", Help: "notinstalled", Cards: []ptCard{
			{ID: "i_download", Img: "u-flexdex-release"},
			{ID: "i_notif", Img: "v-app-tester-notifications"},
			{ID: "i_wait", Img: "w-downloading"},
			{ID: "i_unknown", Img: "y2-app-tester-allow", Warn: true},
			{ID: "i_install", Img: "y4-install-flexdex"},
			{ID: "i_open", Img: "y5-installed-open"},
		}},
		{ID: "first", Help: "bubble", Cards: []ptCard{
			{ID: "f_notif", Img: "y6-flexdex-notifications"},
			{ID: "f_login", Img: "y7-flexdex-login"},
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
