package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

const CookieName = "hgo_session"
const sessionTTL = 30 * 24 * time.Hour

// SuperadminUser is the username that always has admin privileges regardless
// of their DB role. Set from SUPERADMIN_USER env var at startup.
var SuperadminUser string

type User struct {
	ID        uint
	Username  string
	Email     string
	Role      string
	Disabled  bool
	APIAccess bool
	Lang      string
}

func (u *User) IsSuperAdmin() bool {
	return SuperadminUser != "" && u.Username == SuperadminUser
}

func (u *User) IsAdmin() bool {
	return u.Role == "admin" || u.IsSuperAdmin()
}

func (u *User) IsMod() bool {
	return u.Role == "moderator" || u.IsAdmin()
}

func (u *User) IsTester() bool {
	return u.Role == "tester"
}

// HasAPIAccess returns true for superadmins (always) and admins explicitly granted API access.
func (u *User) HasAPIAccess() bool {
	return u.IsSuperAdmin() || (u.IsAdmin() && u.APIAccess)
}

func CreateSession(db *sql.DB, userID uint) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: rand: %w", err)
	}
	token := hex.EncodeToString(b)

	_, err := db.Exec(
		`INSERT INTO sessions (token, user_id, expires_at) VALUES (?, ?, ?)`,
		token, userID, time.Now().Add(sessionTTL),
	)
	if err != nil {
		return "", fmt.Errorf("auth: create session: %w", err)
	}
	return token, nil
}

func GetSession(db *sql.DB, token string) (*User, error) {
	var u User
	err := db.QueryRow(`
		SELECT u.id, u.username, u.email, u.role, u.disabled, u.api_access, COALESCE(u.lang,'en')
		FROM sessions s JOIN users u ON s.user_id = u.id
		WHERE s.token = ? AND s.expires_at > NOW()`,
		token,
	).Scan(&u.ID, &u.Username, &u.Email, &u.Role, &u.Disabled, &u.APIAccess, &u.Lang)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("auth: get session: %w", err)
	}
	if u.Disabled {
		return nil, nil
	}
	return &u, nil
}

func DeleteSession(db *sql.DB, token string) error {
	_, err := db.Exec(`DELETE FROM sessions WHERE token = ?`, token)
	return err
}
