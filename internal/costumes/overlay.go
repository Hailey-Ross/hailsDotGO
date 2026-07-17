package costumes

// Runtime naming of costumes, layered over the embedded labels.json.
//
// labels.json is go:embed'ed, so a running server cannot write it. This is the same problem the
// translator workspace has with the locale files, and this is the same solution: a sparse overlay
// file on disk, merged over the embedded defaults, hot-swapped into memory on write, and PR'd back
// into the repo so the committed file stays the source of truth. See internal/i18n/i18n.go.
//
// The overlay only ever ADDS. A costume label is user data (user_shinies.costume is free text that
// trainers type), so renaming or removing one orphans the art on entries people have already saved.
// Name() refuses any code that already has a label; there is no rename.

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxLabelLen = 64 // matches user_shinies.costume VARCHAR(64)

// overlayFile is the sparse on-disk state: only what admins added.
type overlayFile struct {
	Shared []overlayEntry `json:"shared"`
	Hidden []string       `json:"hidden"`
}

type overlayEntry struct {
	Label string `json:"label"`
	Code  string `json:"code"`
	By    string `json:"by,omitempty"`
	At    string `json:"at,omitempty"`
}

// Init loads the overlay from costumesDir (default "costumes", so /opt/hailsdotgo/costumes in
// production, where systemd sets the working directory).
//
// The directory is gitignored and is not in deploy.ps1's upload list, and deploy never deletes, so
// names added through the admin panel survive a deploy. Parse errors are logged and skipped, never
// fatal: a corrupt overlay must not take the site down, it must just fall back to the embedded set.
func Init(costumesDir string) {
	if costumesDir == "" {
		costumesDir = "costumes"
	}

	mu.Lock()
	defer mu.Unlock()

	dir = costumesDir
	// A full reload: drop whatever was loaded before. Without this, pointing Init at a different
	// (or emptied) directory would leave the previous overlay stranded in memory, so the process
	// would keep serving labels that are no longer on disk.
	ov = overlayFile{}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("costumes: create overlay dir %q: %v", dir, err)
		rebuildLocked()
		return
	}

	data, err := os.ReadFile(overlayPath())
	if os.IsNotExist(err) {
		rebuildLocked()
		return
	}
	if err != nil {
		log.Printf("costumes: read overlay: %v", err)
		rebuildLocked()
		return
	}

	var next overlayFile
	if err := json.Unmarshal(data, &next); err != nil {
		log.Printf("costumes: parse overlay %s: %v (skipped)", overlayPath(), err)
		rebuildLocked()
		return
	}

	// Drop entries whose code no longer has art upstream. The catalog is the authority on what
	// exists; an overlay entry pointing at a vanished code would resolve to a 404 sprite.
	kept := next.Shared[:0]
	for _, e := range next.Shared {
		if _, ok := cat.Codes[e.Code]; ok {
			kept = append(kept, e)
		} else {
			log.Printf("costumes: overlay label %q -> %s has no art upstream (skipped)", e.Label, e.Code)
		}
	}
	next.Shared = kept

	ov = next
	rebuildLocked()
	if n := len(ov.Shared); n > 0 {
		log.Printf("costumes: %d label(s) from the overlay", n)
	}
}

func overlayPath() string { return filepath.Join(dir, "labels.json") }

// rebuildLocked recomputes the merged label set. Callers must hold mu.
//
// Curated labels are appended FIRST, so they win: resolution takes the first shared entry whose
// code covers the species, and an admin must never be able to shadow a curated label.
func rebuildLocked() {
	next := labelSet{
		Comment:                lab.Comment,
		Species:                lab.Species,
		Aliases:                lab.Aliases,
		HiddenComment:          lab.HiddenComment,
		NameCheckIgnoreComment: lab.NameCheckIgnoreComment,
		NameCheckIgnore:        lab.NameCheckIgnore,
	}
	next.Shared = append(append([]shared{}, lab.Shared...), overlayShared(lab.Shared, ov.Shared)...)
	next.Hidden = append(append([]string{}, lab.Hidden...), overlayHidden(lab.Hidden, ov.Hidden)...)

	effective = &next
}

// Name gives an unnamed costume a label, making it selectable in the picker immediately.
func Name(code, label, by string) error {
	label = strings.TrimSpace(label)
	if label == "" {
		return fmt.Errorf("the label is empty")
	}
	if len(label) > maxLabelLen {
		return fmt.Errorf("the label is longer than %d characters", maxLabelLen)
	}

	mu.Lock()
	defer mu.Unlock()
	if dir == "" {
		return fmt.Errorf("the overlay directory is not initialised")
	}

	entry, ok := cat.Codes[code]
	if !ok {
		return fmt.Errorf("%s has no shiny art upstream", code)
	}
	if labelledLocked(code) {
		// Enforced here and not just hidden in the UI: this is the invariant that keeps every
		// existing costume entry working.
		return fmt.Errorf("%s already has a label; labels are never renamed", code)
	}
	if err := collisionLocked(label, entry.Dex); err != nil {
		return err
	}

	next := overlayFile{
		Shared: append(append([]overlayEntry{}, ov.Shared...), overlayEntry{
			Label: label,
			Code:  code,
			By:    by,
			At:    time.Now().UTC().Format(time.RFC3339),
		}),
		Hidden: ov.Hidden,
	}
	return commitLocked(next)
}

// Hide drops a code out of the review queue. Some codes the game flags as costumes are not
// costumes a trainer would record (the Gimmighoul coins), and nagging about them forever is how a
// review queue gets ignored.
func Hide(code string) error {
	mu.Lock()
	defer mu.Unlock()
	if dir == "" {
		return fmt.Errorf("the overlay directory is not initialised")
	}
	if _, ok := cat.Codes[code]; !ok {
		return fmt.Errorf("%s is not in the catalog", code)
	}
	if labelledLocked(code) {
		return fmt.Errorf("%s has a label; unname it first", code)
	}
	for _, h := range effective.Hidden {
		if h == code {
			return nil // already hidden
		}
	}
	return commitLocked(overlayFile{
		Shared: ov.Shared,
		Hidden: append(append([]string{}, ov.Hidden...), code),
	})
}

// Unname removes a label this package added. It is the escape hatch for a typo, and it is ONLY
// safe while nothing uses the label: the caller must have checked user_shinies first, because
// removing a label that trainers have recorded orphans the art on their entries.
//
// It can only ever remove an overlay entry. A curated label from labels.json is not removable.
func Unname(code string) error {
	mu.Lock()
	defer mu.Unlock()
	if dir == "" {
		return fmt.Errorf("the overlay directory is not initialised")
	}

	next := overlayFile{Hidden: ov.Hidden}
	found := false
	for _, e := range ov.Shared {
		if e.Code == code {
			found = true
			continue
		}
		next.Shared = append(next.Shared, e)
	}
	if !found {
		return fmt.Errorf("%s was not named here, so it cannot be unnamed", code)
	}
	return commitLocked(next)
}

// LabelOf returns the overlay label for a code, if this package added one.
func LabelOf(code string) string {
	mu.RLock()
	defer mu.RUnlock()
	for _, e := range ov.Shared {
		if e.Code == code {
			return e.Label
		}
	}
	return ""
}

// commitLocked writes the overlay to disk and only then swaps it into memory, so a failed write
// leaves the running site exactly as it was. Callers must hold mu.
func commitLocked(next overlayFile) error {
	if err := backupLocked(); err != nil {
		return err
	}
	if err := writeOverlay(overlayPath(), next); err != nil {
		return err
	}
	ov = next
	rebuildLocked()
	return nil
}

func backupLocked() error {
	prev, err := os.ReadFile(overlayPath())
	if os.IsNotExist(err) {
		return nil // nothing to back up yet
	}
	if err != nil {
		return fmt.Errorf("read the current overlay: %w", err)
	}
	backupDir := filepath.Join(dir, "backup")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return fmt.Errorf("create the backup dir: %w", err)
	}
	name := fmt.Sprintf("labels_%s.json", time.Now().UTC().Format("20060102-150405"))
	if err := os.WriteFile(filepath.Join(backupDir, name), prev, 0o644); err != nil {
		return fmt.Errorf("write the backup: %w", err)
	}
	return nil
}

// writeOverlay writes via a temp file and rename, so a crash mid-write cannot leave a truncated
// overlay that fails to parse on the next boot.
func writeOverlay(path string, f overlayFile) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal the overlay: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write the overlay: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("replace the overlay: %w", err)
	}
	return nil
}

// labelledLocked reports whether any label already points at this code.
func labelledLocked(code string) bool {
	for _, byLabel := range effective.Species {
		for _, c := range byLabel {
			if c == code {
				return true
			}
		}
	}
	for _, s := range effective.Shared {
		if s.Code == code {
			return true
		}
	}
	return false
}

// collisionLocked rejects a label that would change what an existing label resolves to.
//
// A duplicate label is legitimate when the two codes cover DIFFERENT species: "Party Hat" already
// maps to both c:JAN_2020_NOEVOLVE and c:ANNIVERSARY_2024, and resolution simply takes the first
// entry whose code has art for that species. But if the species sets overlap, the new entry is
// unreachable for those species (or worse, shadows the old one), so refuse it.
func collisionLocked(label string, dex []int) error {
	want := make(map[int]bool, len(dex))
	for _, d := range dex {
		want[d] = true
	}

	for _, s := range effective.Shared {
		if !strings.EqualFold(s.Label, label) {
			continue
		}
		e, ok := cat.Codes[s.Code]
		if !ok {
			continue
		}
		for _, d := range e.Dex {
			if want[d] {
				return fmt.Errorf("%q is already used for %s, which also covers dex %d; pick a different label",
					s.Label, s.Code, d)
			}
		}
	}

	for species, byLabel := range effective.Species {
		code, ok := byLabel[label]
		if !ok {
			continue
		}
		if e, ok := cat.Codes[code]; ok {
			for _, d := range e.Dex {
				if want[d] {
					return fmt.Errorf("%q is already used for %s on %s; pick a different label", label, code, species)
				}
			}
		}
	}
	return nil
}
