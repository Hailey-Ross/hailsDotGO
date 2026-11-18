package handlers

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"pogo.hails.cc/internal/auth"
)

// roleDB is a tiny in-memory stand-in for MySQL that answers only the three statements
// AdminChangeRole runs: the session lookup, the target lookup and the role update. That is enough
// to drive the real handler end to end without a database server.
type roleDB struct {
	mu       sync.Mutex
	sessions map[string]auth.User // token -> caller
	users    map[int64][2]string  // id -> username, role
	updates  int
}

type roleConn struct{ db *roleDB }
type roleStmt struct {
	db    *roleDB
	query string
}
type roleRows struct {
	cols []string
	rows [][]driver.Value
	i    int
}

func (c *roleConn) Prepare(q string) (driver.Stmt, error) { return &roleStmt{db: c.db, query: q}, nil }
func (c *roleConn) Close() error                          { return nil }
func (c *roleConn) Begin() (driver.Tx, error)             { return nil, fmt.Errorf("no transactions") }

func (s *roleStmt) Close() error  { return nil }
func (s *roleStmt) NumInput() int { return -1 }

func (s *roleStmt) Exec(args []driver.Value) (driver.Result, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	if strings.Contains(s.query, "UPDATE users SET role") {
		id := args[1].(int64)
		u := s.db.users[id]
		s.db.users[id] = [2]string{u[0], args[0].(string)}
		s.db.updates++
		return driver.RowsAffected(1), nil
	}
	// last_seen_at bumps and the like: accept and ignore.
	return driver.RowsAffected(0), nil
}

func (s *roleStmt) Query(args []driver.Value) (driver.Rows, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	switch {
	case strings.Contains(s.query, "FROM sessions s JOIN users u"):
		u, ok := s.db.sessions[args[0].(string)]
		if !ok {
			return &roleRows{cols: make([]string, 10)}, nil
		}
		return &roleRows{cols: make([]string, 10), rows: [][]driver.Value{{int64(u.ID), u.Username, "", true, u.Role, false, false, false, "", "en"}}}, nil
	case strings.Contains(s.query, "SELECT username, role FROM users WHERE id"):
		u, ok := s.db.users[args[0].(int64)]
		if !ok {
			return &roleRows{cols: []string{"username", "role"}}, nil
		}
		return &roleRows{cols: []string{"username", "role"}, rows: [][]driver.Value{{u[0], u[1]}}}, nil
	}
	return nil, fmt.Errorf("roleDB: unexpected query %q", s.query)
}

func (r *roleRows) Columns() []string { return r.cols }
func (r *roleRows) Close() error      { return nil }
func (r *roleRows) Next(dest []driver.Value) error {
	if r.i >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.i])
	r.i++
	return nil
}

type roleConnector struct{ db *roleDB }

func (c roleConnector) Connect(context.Context) (driver.Conn, error) { return &roleConn{db: c.db}, nil }
func (c roleConnector) Driver() driver.Driver                        { return roleDriver{} }

type roleDriver struct{}

func (roleDriver) Open(string) (driver.Conn, error) { return nil, fmt.Errorf("use the connector") }

// TestAdminChangeRoleRankGate: staff may change the role of someone strictly below them only. An
// Admin changing another Admin's role is refused with 403 and nothing is written; an Admin changing
// a Moderator's role goes through.
func TestAdminChangeRoleRankGate(t *testing.T) {
	prev := auth.SuperadminUser
	auth.SuperadminUser = "owner"
	t.Cleanup(func() { auth.SuperadminUser = prev })

	cases := []struct {
		name       string
		targetRole string
		wantStatus int
		wantWrite  bool
	}{
		{"admin on peer admin", "admin", http.StatusForbidden, false},
		{"admin on moderator", "moderator", http.StatusOK, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rdb := &roleDB{
				sessions: map[string]auth.User{"tok-admin": {ID: 1, Username: "alice", Role: "admin"}},
				users:    map[int64][2]string{1: {"alice", "admin"}, 2: {"bob", c.targetRole}},
			}
			db := sql.OpenDB(roleConnector{db: rdb})
			defer db.Close()
			h := &Handlers{db: db}

			req := httptest.NewRequest(http.MethodPost, "/admin/users/2/role", strings.NewReader(`{"role":"user"}`))
			req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: "tok-admin"})
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("id", "2")
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
			rec := httptest.NewRecorder()

			h.AdminChangeRole(rec, req)

			if rec.Code != c.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, c.wantStatus, rec.Body.String())
			}
			if (rdb.updates > 0) != c.wantWrite {
				t.Fatalf("role written = %v, want %v", rdb.updates > 0, c.wantWrite)
			}
		})
	}
}
