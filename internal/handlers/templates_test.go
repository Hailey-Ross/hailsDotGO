package handlers

import (
	"html/template"
	"path/filepath"
	"testing"
)

// TestTemplatesParse parses every page template the way loadTemplates does.
//
// loadTemplates calls log.Fatalf on a parse error, so a stray {{end}} or an unbalanced action does
// not fail a build or a test: it kills the service at boot, in production, after a deploy. This
// turns that into a normal test failure.
func TestTemplatesParse(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	// t.Chdir, not os.Chdir: the process wide directory is restored when this test
	// ends. A bare os.Chdir left every test that ran afterwards looking at the repo
	// root, so a relative path in one of them resolved somewhere else entirely and
	// failed only when the whole package ran.
	t.Chdir(root)

	pages := []string{
		"home", "raids", "dps", "pvp", "events", "iv", "box", "credits", "maintenance",
		"login", "register", "shinies", "admin", "settings", "trainers", "store",
		"translate", "raidfinder", "trainer", "social", "notifications", "reports",
		"forgot_password", "reset_password", "verify_email",
	}
	for _, page := range pages {
		if _, err := template.New("base.html").Funcs(tmplFuncs).ParseFiles(
			filepath.Join("templates", "base.html"),
			filepath.Join("templates", page+".html"),
		); err != nil {
			t.Errorf("template %q does not parse: %v", page, err)
		}
	}
}
