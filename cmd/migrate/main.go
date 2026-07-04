// Command migrate is the upgrade tool for existing hailsDotGO databases.
//
// It reads migrate.sql, tracks applied sections in schema_migrations, and runs
// only the pending ones. Fresh installs use schema.sql and do NOT need this.
//
// Usage:
//
//	migrate -status                 show applied vs pending sections
//	migrate -from v0.1.4a           baseline an untracked install, then apply the rest
//	migrate -dry-run                show what would run, change nothing
//	migrate -to 40                  apply only up to section 40
//	migrate -baseline current       record all sections as applied without running them
//	migrate -dump-seed              print the schema_migrations seed block for schema.sql
//
// Database connection comes from the same env vars as the app (DB_HOST,
// DB_USER, DB_PASS, DB_NAME); export your .env before running.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	appdb "pogo.hails.cc/internal/db"
	"pogo.hails.cc/internal/migrate"
)

func main() {
	var (
		file     = flag.String("file", "migrate.sql", "path to migrate.sql")
		status   = flag.Bool("status", false, "show applied vs pending sections and exit")
		dryRun   = flag.Bool("dry-run", false, "show what would be applied without changing anything")
		from     = flag.String("from", "", "baseline an untracked install at this version (e.g. v0.1.4a), then apply the rest")
		baseline = flag.String("baseline", "", "record sections as applied WITHOUT running them: a version (e.g. v0.1.4a) or 'current'")
		to       = flag.Int("to", 0, "apply only up to and including this section number (0 = all)")
		yes      = flag.Bool("yes", false, "skip the confirmation prompt")
		dumpSeed = flag.Bool("dump-seed", false, "print the schema_migrations seed INSERT for schema.sql and exit")
	)
	flag.Parse()

	sections, err := migrate.ParseFile(*file)
	if err != nil {
		fatal(err)
	}

	if *dumpSeed {
		printSeed(sections)
		return
	}

	db, err := appdb.Open()
	if err != nil {
		fatal(err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		fatal(fmt.Errorf("db ping (check DB_HOST/DB_USER/DB_PASS/DB_NAME): %w", err))
	}

	// A fresh database has no users table; it should use schema.sql, not this tool.
	hasUsers, err := migrate.TableExists(db, "users")
	if err != nil {
		fatal(err)
	}
	if !hasUsers {
		fatal(fmt.Errorf("no 'users' table found: this looks like a fresh database.\n" +
			"Run schema.sql for a new install; migrate is only for upgrading existing installs"))
	}

	if err := migrate.EnsureTable(db); err != nil {
		fatal(err)
	}

	applied, err := migrate.AppliedSections(db)
	if err != nil {
		fatal(err)
	}

	// Baseline-only mode: record and exit, run nothing.
	if *baseline != "" {
		through, err := resolveThrough(*baseline, sections)
		if err != nil {
			fatal(err)
		}
		if err := migrate.RecordThrough(db, through, sections); err != nil {
			fatal(err)
		}
		fmt.Printf("Baselined: recorded sections 1-%d as applied (no SQL executed).\n", through)
		return
	}

	// First contact with an untracked, populated install: require -from so we
	// never blast all 40 sections at a database that is partway upgraded.
	if len(applied) == 0 && *from == "" {
		fmt.Fprintln(os.Stderr, "This database has no migration history yet.")
		fmt.Fprintln(os.Stderr, "Re-run with -from <your installed version> so the tool knows what is already applied, e.g.:")
		fmt.Fprintln(os.Stderr, "    migrate -from v0.1.4a")
		fmt.Fprintln(os.Stderr, "\nKnown versions and the highest section each had applied:")
		for _, v := range migrate.KnownVersions() {
			fmt.Fprintf(os.Stderr, "    %-8s -> through section %d\n", v, migrate.BaselineVersions[v])
		}
		fmt.Fprintln(os.Stderr, "\n(If every section is already applied by hand, use: migrate -baseline current)")
		os.Exit(1)
	}

	// Baseline from a version before applying the remainder.
	if *from != "" {
		if len(applied) > 0 {
			fmt.Println("Note: migration history already exists; -from is ignored.")
		} else {
			through, err := resolveThrough(*from, sections)
			if err != nil {
				fatal(err)
			}
			if err := migrate.RecordThrough(db, through, sections); err != nil {
				fatal(err)
			}
			fmt.Printf("Baselined at %s: sections 1-%d marked applied.\n", migrate.NormalizeVersion(*from), through)
			applied, err = migrate.AppliedSections(db)
			if err != nil {
				fatal(err)
			}
		}
	}

	// Work out the pending set, honouring -to.
	var pending []migrate.Section
	for _, s := range sections {
		if applied[s.Num] {
			continue
		}
		if *to > 0 && s.Num > *to {
			continue
		}
		pending = append(pending, s)
	}

	if *status {
		printStatus(sections, applied)
		return
	}

	if len(pending) == 0 {
		fmt.Println("Up to date: no pending migrations.")
		return
	}

	fmt.Printf("Pending migrations (%d):\n", len(pending))
	for _, s := range pending {
		fmt.Printf("  %3d. %s  (%d statement(s))\n", s.Num, s.Title, len(s.Stmts))
	}

	if *dryRun {
		fmt.Println("\nDry run: nothing was applied.")
		return
	}

	if !*yes && !confirm(fmt.Sprintf("\nApply these %d migration(s)?", len(pending))) {
		fmt.Println("Aborted.")
		return
	}

	for _, s := range pending {
		skipped, err := migrate.ApplySection(db, s)
		if err != nil {
			fatal(err)
		}
		note := ""
		if skipped > 0 {
			note = fmt.Sprintf(" (%d statement(s) already applied)", skipped)
		}
		fmt.Printf("  applied %3d. %s%s\n", s.Num, s.Title, note)
	}
	fmt.Printf("\nDone: %d migration(s) applied.\n", len(pending))
}

// resolveThrough turns a version string or "current" into a section number.
func resolveThrough(v string, sections []migrate.Section) (int, error) {
	if strings.EqualFold(strings.TrimSpace(v), "current") {
		return sections[len(sections)-1].Num, nil
	}
	nv := migrate.NormalizeVersion(v)
	through, ok := migrate.BaselineVersions[nv]
	if !ok {
		return 0, fmt.Errorf("unknown version %q; known: %s (or 'current')", v, strings.Join(migrate.KnownVersions(), ", "))
	}
	return through, nil
}

func printStatus(sections []migrate.Section, applied map[int]bool) {
	var done, pend int
	for _, s := range sections {
		mark := "pending"
		if applied[s.Num] {
			mark = "applied"
			done++
		} else {
			pend++
		}
		fmt.Printf("  [%s] %3d. %s\n", mark, s.Num, s.Title)
	}
	// Flag history rows that no longer have a matching section (e.g. a removed header).
	known := map[int]bool{}
	for _, s := range sections {
		known[s.Num] = true
	}
	var orphans []int
	for n := range applied {
		if !known[n] {
			orphans = append(orphans, n)
		}
	}
	sort.Ints(orphans)
	for _, n := range orphans {
		fmt.Printf("  [orphan] %3d. (recorded but not in migrate.sql)\n", n)
	}
	fmt.Printf("\n%d applied, %d pending, %d total.\n", done, pend, len(sections))
}

// printSeed emits the schema_migrations seed block to paste into schema.sql so
// fresh installs start with a fully-recorded migration history.
func printSeed(sections []migrate.Section) {
	fmt.Println("-- Migration history baseline (regenerate with: migrate -dump-seed)")
	fmt.Println("CREATE TABLE IF NOT EXISTS schema_migrations (")
	fmt.Println("  section    INT UNSIGNED NOT NULL,")
	fmt.Println("  name       VARCHAR(160) NOT NULL DEFAULT '',")
	fmt.Println("  applied_at DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,")
	fmt.Println("  PRIMARY KEY (section)")
	fmt.Println(") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;")
	fmt.Println()
	fmt.Println("INSERT IGNORE INTO schema_migrations (section, name) VALUES")
	for i, s := range sections {
		sep := ","
		if i == len(sections)-1 {
			sep = ";"
		}
		fmt.Printf("  (%d, %s)%s\n", s.Num, sqlQuote(s.Title), sep)
	}
}

func sqlQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func confirm(prompt string) bool {
	fmt.Printf("%s [y/N] ", prompt)
	sc := bufio.NewScanner(os.Stdin)
	if !sc.Scan() {
		return false
	}
	resp := strings.ToLower(strings.TrimSpace(sc.Text()))
	return resp == "y" || resp == "yes"
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
