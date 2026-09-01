package handlers

// Account deletion, in two stages: a mark, then a purge ninety days later.
//
// Deleting an account used to be one irreversible DELETE. It is now a mark on the row
// (users.deleted_at) and a sweep that removes the row for real once the retention window has
// passed. To everyone using the site the mark IS the deletion: the account stops resolving in
// every read path, its sessions are dropped, and it can no longer log in. What changes is that
// the row survives underneath for a while.
//
// Two things follow from the window that are easy to get wrong:
//
//   - A marked account KEEPS its username and email. The unique keys stay in place, so nobody
//     can register under a former trainer's name while the window is open, and an accidental
//     deletion can be undone with restoreUser. The names free up when the purge runs.
//   - Every read path has to exclude marked accounts, and nothing in SQL enforces that. The
//     codebase already repeats `disabled = 0` by hand in twenty places; this adds a second
//     clause to each of them. A missed one does not error, it renders a ghost, so
//     TestNoUserLookupForgetsTheDeletedClause pins the pair together at the source level.
//
// The purge is the ONLY place a user row is destroyed, and it reuses the same cleanup the old
// delete did, because those statements are not about deletion policy: they null references that
// have no foreign key behind them and would otherwise point at an id that no longer exists.
//
// Three admin actions sit on top of this, all superadmin only: mark (AdminDeleteUser), lift the
// mark (AdminRestoreUser), and destroy the row now without waiting out the window
// (AdminPurgeUser). The last one exists because /privacy promises a trainer who asks for erasure
// that they get erasure, and a row held for ninety days is not that. Moderation deletions take
// the window and keep their undo; an erasure request skips it.

import (
	"database/sql"
	"errors"
	"log"
	"time"
)

// userPurgeAfter is how long a marked account is retained before its row is destroyed.
//
// Ninety days, and deliberately not stated on the privacy page. Note that the mark is what a
// trainer experiences as deletion: this window is invisible to them and to every other user.
const userPurgeAfter = 90 * 24 * time.Hour

// userPurgeInterval is how often the sweep looks for expired marks. Daily is far more often than
// needed for a ninety day window; it is cheap because the index makes the query a range scan that
// usually matches nothing.
const userPurgeInterval = 24 * time.Hour

// errUserNotMarked is returned when a purge is asked to destroy a row that carries no deletion
// mark. It is a refusal, not a failure: the mark is the only authority the purge acts on, so a
// row without one is a row nobody has deleted.
var errUserNotMarked = errors.New("account is not marked for deletion")

// StartUserPurge runs the retention sweep on a ticker for the life of the process.
//
// Deliberately does NOT sweep at startup. A restart loop would otherwise turn into a purge loop,
// and nothing here is urgent: an account one day past its window can wait for the next tick.
func (h *Handlers) StartUserPurge() {
	go func() {
		for range time.NewTicker(userPurgeInterval).C {
			if n, err := h.purgeExpiredUsers(); err != nil {
				log.Printf("user purge: %v", err)
			} else if n > 0 {
				log.Printf("user purge: removed %d account(s) past the retention window", n)
			}
		}
	}()
}

// purgeExpiredUsers destroys every account whose mark is older than the retention window.
func (h *Handlers) purgeExpiredUsers() (int, error) {
	cutoff := time.Now().UTC().Add(-userPurgeAfter)

	// Collected first rather than deleted in one statement, because each row needs the cleanup
	// below run against its own id, and because the log line naming what went is the only lasting
	// record that it happened.
	rows, err := h.db.Query(
		`SELECT id, username FROM users WHERE deleted_at IS NOT NULL AND deleted_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	type doomed struct {
		id       uint64
		username string
	}
	var list []doomed
	for rows.Next() {
		var d doomed
		if rows.Scan(&d.id, &d.username) == nil {
			list = append(list, d)
		}
	}
	rows.Close()

	purged := 0
	for _, d := range list {
		// One transaction per account, so a single bad row cannot strand the rest half done.
		if err := h.purgeOneUser(d.id, d.username); err != nil {
			log.Printf("user purge: %d (%q): %v", d.id, d.username, err)
			continue
		}
		log.Printf("user purge: destroyed user %d (%q), marked more than %d days ago",
			d.id, d.username, int(userPurgeAfter.Hours()/24))
		purged++
	}
	return purged, nil
}

// purgeOneUser is the real, irreversible delete. It refuses an account that is not marked.
func (h *Handlers) purgeOneUser(id uint64, username string) error {
	tx, err := h.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := cleanupUserReferences(tx, id); err != nil {
		return err
	}
	// Matched on the username as well as the id, exactly as the old delete was, so the row that
	// goes is the row that was looked at.
	//
	// `deleted_at IS NOT NULL` is what makes it structurally impossible to destroy a live account
	// here. The sweep already selects only marked rows, so it is redundant on that path, but the
	// Erase Now button reaches this same function with an id handed to it by a browser, and the
	// mark is the thing that makes such an id safe to act on. An unmarked row gets a rollback.
	res, err := tx.Exec(
		`DELETE FROM users WHERE id = ? AND username = ? AND deleted_at IS NOT NULL`, id, username)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errUserNotMarked
	}
	return tx.Commit()
}

// cleanupUserReferences nulls or removes the references that no foreign key covers.
//
// Lifted verbatim from the old AdminDeleteUser. It is not deletion policy: every statement here
// exists because the column it touches has no foreign key behind it, so nothing nulls it
// automatically and it would be left pointing at an id that has gone.
func cleanupUserReferences(tx *sql.Tx, id uint64) error {
	cleanup := []struct {
		what string
		stmt string
	}{
		// Neither column has a foreign key behind it, so nothing nulls them for us
		// and they would be left pointing at an id that no longer exists.
		{"custom_tag_requests.reviewed_by",
			`UPDATE custom_tag_requests SET reviewed_by = NULL WHERE reviewed_by = ?`},
		{"bug_report_participants.added_by",
			`UPDATE bug_report_participants SET added_by = NULL WHERE added_by = ?`},

		// reporter_id is ON DELETE SET NULL, but reporter_email is a plain string
		// and would outlive the account whose address it is.
		{"bug_reports.reporter_email",
			`UPDATE bug_reports SET reporter_email = NULL WHERE reporter_id = ?`},

		// trust_events.lobby_id has no foreign key to raid_lobbies, so deleting a
		// host strands other trainers' events. Null the dead reference rather than
		// delete the row: users.trust_score is a stored running sum of
		// applied_delta, so removing those rows would silently desync scores that
		// belong to trainers who are still here. uk_te_vote tolerates the NULL.
		{"trust_events.lobby_id",
			`UPDATE trust_events te JOIN raid_lobbies l ON l.id = te.lobby_id
			    SET te.lobby_id = NULL
			  WHERE l.host_id = ?`},

		// raid_ratings.post_id has no foreign key to raid_posts and is NOT NULL, so
		// unlike the events above these rows cannot be kept once the host's posts
		// cascade away. Nothing caches them: AdminClearRatings deletes the same
		// rows with no recompute afterwards.
		{"raid_ratings orphaned by raid_posts",
			`DELETE r FROM raid_ratings r
			   JOIN raid_posts p ON p.id = r.post_id
			  WHERE p.user_id = ?`},
	}
	for _, c := range cleanup {
		if _, err := tx.Exec(c.stmt, id); err != nil {
			log.Printf("purge user %d: cleanup %s: %v", id, c.what, err)
			return err
		}
	}
	return nil
}

// markUserDeleted is the deletion a superadmin actually performs.
//
// The row stays; deleted_at is what removes the account from the site. Sessions go with it, so a
// marked trainer is signed out everywhere rather than continuing until their token expires.
//
// The `deleted_at IS NULL` in the WHERE makes this idempotent in the way that matters: marking an
// already marked account must not restart its retention clock.
func (h *Handlers) markUserDeleted(id uint64, username string) (int64, error) {
	res, err := h.db.Exec(
		`UPDATE users SET deleted_at = UTC_TIMESTAMP() WHERE id = ? AND username = ? AND deleted_at IS NULL`,
		id, username)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		// Best effort. A leftover session cannot authenticate anyway, because GetSession joins
		// users and now requires deleted_at IS NULL, so a failure here is untidy rather than
		// unsafe.
		if _, err := h.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
			log.Printf("mark user %d deleted: clearing sessions: %v", id, err)
		}
	}
	return n, nil
}

// restoreUser lifts a deletion mark, which is the entire reason the row is kept at all.
//
// The account comes back as it was. Nothing is moved or archived on the way out, so clearing the
// one column really is all there is to it, and every read path starts resolving the account again
// on the next query.
//
// Sessions do NOT come back: marking dropped them and this does not recreate them, so a restored
// trainer signs in again. That is the right outcome rather than a shortcoming, because an account
// deleted in error should not have a live session left over from before it went.
//
// `deleted_at IS NOT NULL` means restoring an account that was never marked reports zero rows
// instead of quietly succeeding, so the panel can say so rather than imply it undid something.
func (h *Handlers) restoreUser(id uint64, username string) (int64, error) {
	res, err := h.db.Exec(
		`UPDATE users SET deleted_at = NULL WHERE id = ? AND username = ? AND deleted_at IS NOT NULL`,
		id, username)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
