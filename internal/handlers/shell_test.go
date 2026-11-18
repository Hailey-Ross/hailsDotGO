package handlers

import (
	"bytes"
	"html/template"
	"path/filepath"
	"strings"
	"testing"

	"pogo.hails.cc/internal/auth"
	"pogo.hails.cc/internal/i18n"
)

// TestShellRenders executes the Field Kit shell in base.html for a visitor, a trainer and a staff
// member. Parsing alone (TestTemplatesParse) misses execution errors such as a call on a nil
// User, and an execution error in base.html breaks every page on the site.
func TestShellRenders(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	t.Chdir(root)

	tmpl, err := template.New("base.html").Funcs(tmplFuncs).ParseFiles(filepath.Join("templates", "base.html"))
	if err != nil {
		t.Fatalf("parse base.html: %v", err)
	}
	all := PageMaintenance{RaidsEnabled: true, DPSEnabled: true, PVPEnabled: true, EventsEnabled: true, IVEnabled: true,
		TrainersEnabled: true, TrainerDirectoryEnabled: true, RaidFinderEnabled: true, ShiniesEnabled: true}

	cases := []struct {
		name    string
		pd      PageData
		want    []string
		notWant []string
	}{
		{
			name:    "visitor",
			pd:      PageData{Maintenance: all, Lang: "en", Langs: []string{"en", "de"}, Path: "/raids"},
			want:    []string{`class="fk-dock"`, `data-slot="raids" href="/raids" aria-current="page"`, `href="/login"`, `id="fk-tpl-more"`, `value="de"`, `data-page="legacy"`},
			notWant: []string{`/logout`, `nav-notif-badge`, `href="/admin"`},
		},
		{
			name: "trainer",
			pd: PageData{User: &auth.User{Username: "Élan", Role: "user"}, Maintenance: all, Lang: "en", Path: "/dps",
				ReportCount: 2, ReportUnread: 1},
			want:    []string{`<span class="fk-av" aria-hidden="true">É</span>`, `action="/logout"`, `id="nav-notif-badge"`, `href="/social/%c3%89lan"`, `href="/reports"`, `id="nav-bug-btn"`},
			notWant: []string{`href="/admin"`},
		},
		{
			name: "admin",
			pd: PageData{User: &auth.User{Username: "staff", Role: "admin", Translator: true}, Maintenance: all, Lang: "en", Path: "/admin",
				CostumeReviewCount: 3, StoreEnabled: true},
			want: []string{`href="/admin" aria-current="page"`, `<b class="fk-count">3</b>`, `href="/translate"`, `href="/store"`},
		},
		{
			name:    "calc gated",
			pd:      PageData{Maintenance: PageMaintenance{IVEnabled: true}, Lang: "en", Path: "/iv"},
			want:    []string{`href="/iv" aria-current="page"`},
			notWant: []string{`href="/raids"`, `href="/dps"`},
		},
	}
	// The launcher is a restyled page: it must opt out of legacy mode and render its widgets.
	t.Run("home", func(t *testing.T) {
		home, err := template.New("base.html").Funcs(tmplFuncs).ParseFiles(filepath.Join("templates", "base.html"), filepath.Join("templates", "home.html"))
		if err != nil {
			t.Fatal(err)
		}
		home.Funcs(template.FuncMap{"T": i18n.TFunc("en")})
		for _, pd := range []PageData{
			{Maintenance: all, Lang: "en", Path: "/", Data: homeData{}},
			{User: &auth.User{Username: "u"}, Maintenance: all, Lang: "en", Path: "/", Data: homeData{Name: "Trainer Red"}},
		} {
			var buf bytes.Buffer
			if err := home.ExecuteTemplate(&buf, "base", pd); err != nil {
				t.Fatalf("execute: %v", err)
			}
			out := buf.String()
			for _, w := range []string{`data-page="fieldkit"`, `id="fk-hero"`, `id="fk-dex-total"`, `/static/js/home.js`} {
				if !strings.Contains(out, w) {
					t.Errorf("missing %q", w)
				}
			}
			if pd.User != nil && !strings.Contains(out, `"Trainer Red"`) {
				t.Errorf("greeting name not passed to the page")
			}
		}
	})

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clone, err := tmpl.Clone()
			if err != nil {
				t.Fatal(err)
			}
			clone.Funcs(template.FuncMap{"T": i18n.TFunc("en")})
			var buf bytes.Buffer
			if err := clone.ExecuteTemplate(&buf, "base", c.pd); err != nil {
				t.Fatalf("execute: %v", err)
			}
			out := buf.String()
			for _, w := range c.want {
				if !strings.Contains(out, w) {
					t.Errorf("missing %q", w)
				}
			}
			for _, w := range c.notWant {
				if strings.Contains(out, w) {
					t.Errorf("unexpected %q", w)
				}
			}
		})
	}
}
