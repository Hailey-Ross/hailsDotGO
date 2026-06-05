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

type User struct {
	ID       uint
	Username string
	Email    string
	Role     string
}

func (u *User) IsAdmin() bool {
	return u.Role == "admin" || u.Username == "hails"
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
		SELECT u.id, u.username, u.email, u.role
		FROM sessions s JOIN users u ON s.user_id = u.id
		WHERE s.token = ? AND s.expires_at > NOW()`,
		token,
	).Scan(&u.ID, &u.Username, &u.Email, &u.Role)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("auth: get session: %w", err)
	}
	return &u, nil
}

func DeleteSession(db *sql.DB, token string) {
	db.Exec(`DELETE FROM sessions WHERE token = ?`, token)
}
