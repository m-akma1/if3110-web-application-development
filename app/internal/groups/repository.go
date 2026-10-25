// Package groups stores groups, group membership and invite codes on the platform DB.
// Access grants per group live in internal/rbac; this package only knows who is in which group.
package groups

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"strings"
	"time"

	"dbplatform/app/internal/ids"
	"dbplatform/app/internal/platformdb"

	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrGroupNotFound     = errors.New("group tidak ditemukan")
	ErrGroupNameTaken    = errors.New("nama group sudah dipakai")
	ErrInvalidGroupName  = errors.New("nama group harus 2-64 karakter")
	ErrInvalidInviteCode = errors.New("kode undangan tim tidak valid")
	ErrInviteCodeTaken   = errors.New("kode undangan sudah dipakai")
	ErrSystemGroup       = errors.New("group sistem tidak dapat diubah dengan cara ini")
)

// PublicGroupID is the system group every user belongs to.
const PublicGroupID = platformdb.PublicGroupID

// Group is a set of users that receives database grants.
type Group struct {
	ID          string
	Name        string
	Description string
	IsSystem    bool
	MemberCount int
	CreatedAt   time.Time
}

// InviteCode lets a new user join a group at signup.
type InviteCode struct {
	Code      string
	GroupID   string
	GroupName string
	IsActive  bool
	CreatedAt time.Time
}

// Repository is the PostgreSQL store for groups, members and invite codes.
type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && (constraint == "" || pgErr.ConstraintName == constraint)
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// Create adds a new (non-system) group.
func (r *Repository) Create(ctx context.Context, name, description string) (*Group, error) {
	name = strings.TrimSpace(name)
	if n := len([]rune(name)); n < 2 || n > 64 {
		return nil, ErrInvalidGroupName
	}
	g := &Group{ID: ids.New(), Name: name, Description: strings.TrimSpace(description)}
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO groups (id, name, description) VALUES ($1, $2, $3)
		RETURNING created_at`, g.ID, g.Name, g.Description).Scan(&g.CreatedAt)
	if isUniqueViolation(err, "groups_name_key") {
		return nil, ErrGroupNameTaken
	}
	if err != nil {
		return nil, err
	}
	return g, nil
}

const groupSelect = `
	SELECT g.id, g.name, g.description, g.is_system, g.created_at,
	       (SELECT COUNT(*) FROM group_members gm WHERE gm.group_id = g.id)
	FROM groups g`

func scanGroups(rows *sql.Rows) ([]Group, error) {
	defer rows.Close()
	var list []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.IsSystem, &g.CreatedAt, &g.MemberCount); err != nil {
			return nil, err
		}
		list = append(list, g)
	}
	return list, rows.Err()
}

// List returns every group, system groups first, then by name.
func (r *Repository) List(ctx context.Context) ([]Group, error) {
	rows, err := r.db.QueryContext(ctx, groupSelect+` ORDER BY g.is_system DESC, LOWER(g.name)`)
	if err != nil {
		return nil, err
	}
	return scanGroups(rows)
}

// Get returns one group by id.
func (r *Repository) Get(ctx context.Context, groupID string) (*Group, error) {
	if !ids.IsValid(groupID) {
		return nil, ErrGroupNotFound
	}
	rows, err := r.db.QueryContext(ctx, groupSelect+` WHERE g.id = $1`, groupID)
	if err != nil {
		return nil, err
	}
	list, err := scanGroups(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrGroupNotFound
	}
	return &list[0], nil
}

// GetByName returns one group by case-insensitive name.
func (r *Repository) GetByName(ctx context.Context, name string) (*Group, error) {
	rows, err := r.db.QueryContext(ctx, groupSelect+` WHERE LOWER(g.name) = LOWER($1)`, strings.TrimSpace(name))
	if err != nil {
		return nil, err
	}
	list, err := scanGroups(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrGroupNotFound
	}
	return &list[0], nil
}

// ListForUser returns the groups a user belongs to (always including Public).
func (r *Repository) ListForUser(ctx context.Context, userID string) ([]Group, error) {
	if !ids.IsValid(userID) {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx, groupSelect+`
		JOIN group_members m ON m.group_id = g.id
		WHERE m.user_id = $1
		ORDER BY g.is_system DESC, LOWER(g.name)`, userID)
	if err != nil {
		return nil, err
	}
	return scanGroups(rows)
}

// MemberGroupsByUser returns, for every user, the names of their groups (used by the admin user list).
func (r *Repository) MemberGroupsByUser(ctx context.Context) (map[string][]Group, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT m.user_id, g.id, g.name, g.is_system
		FROM group_members m
		JOIN groups g ON g.id = m.group_id
		ORDER BY g.is_system DESC, LOWER(g.name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]Group{}
	for rows.Next() {
		var userID string
		var g Group
		if err := rows.Scan(&userID, &g.ID, &g.Name, &g.IsSystem); err != nil {
			return nil, err
		}
		out[userID] = append(out[userID], g)
	}
	return out, rows.Err()
}

// AddMember adds a user to a group. Adding an existing member is a no-op.
func (r *Repository) AddMember(ctx context.Context, groupID, userID string) error {
	if !ids.IsValid(groupID) || !ids.IsValid(userID) {
		return ErrGroupNotFound
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO group_members (group_id, user_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, groupID, userID)
	if isForeignKeyViolation(err) {
		return ErrGroupNotFound
	}
	return err
}

// ResolveInviteCode returns the group of an active invite code (case-insensitive).
func (r *Repository) ResolveInviteCode(ctx context.Context, code string) (*Group, error) {
	code = NormalizeCode(code)
	if code == "" {
		return nil, ErrInvalidInviteCode
	}
	var groupID string
	err := r.db.QueryRowContext(ctx,
		`SELECT group_id FROM invite_codes WHERE code = $1 AND is_active`, code).Scan(&groupID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidInviteCode
	}
	if err != nil {
		return nil, err
	}
	return r.Get(ctx, groupID)
}

// NormalizeCode trims and uppercases an invite code.
func NormalizeCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}

// CreateInviteCode stores an active invite code for a group. An empty code generates a random one.
// createdBy may be empty (seed data).
func (r *Repository) CreateInviteCode(ctx context.Context, code, groupID, createdBy string) (*InviteCode, error) {
	code = NormalizeCode(code)
	if code == "" {
		code = RandomCode()
	}
	if !ids.IsValid(groupID) {
		return nil, ErrGroupNotFound
	}
	if groupID == PublicGroupID {
		return nil, ErrSystemGroup
	}
	var creator any
	if ids.IsValid(createdBy) {
		creator = createdBy
	}
	inv := &InviteCode{Code: code, GroupID: groupID, IsActive: true}
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO invite_codes (code, group_id, created_by) VALUES ($1, $2, $3)
		RETURNING created_at`, code, groupID, creator).Scan(&inv.CreatedAt)
	var pgErr *pgconn.PgError
	switch {
	case isUniqueViolation(err, "invite_codes_pkey"):
		return nil, ErrInviteCodeTaken
	case isForeignKeyViolation(err):
		return nil, ErrGroupNotFound
	case errors.As(err, &pgErr) && pgErr.Code == "23514": // check_violation: code format
		return nil, ErrInvalidInviteCode
	case err != nil:
		return nil, err
	}
	return inv, nil
}

// ListInviteCodes returns every invite code with its group name, newest first.
func (r *Repository) ListInviteCodes(ctx context.Context) ([]InviteCode, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT c.code, c.group_id, g.name, c.is_active, c.created_at
		FROM invite_codes c
		JOIN groups g ON g.id = c.group_id
		ORDER BY c.created_at DESC, c.code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []InviteCode
	for rows.Next() {
		var c InviteCode
		if err := rows.Scan(&c.Code, &c.GroupID, &c.GroupName, &c.IsActive, &c.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

// codeAlphabet avoids look-alike characters (0/O, 1/I).
const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// RandomCode returns a random invite code such as "JOIN-7K2Q-MX4P".
func RandomCode() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("groups: crypto/rand failed: " + err.Error())
	}
	out := []byte("JOIN-XXXX-XXXX")
	pos := []int{5, 6, 7, 8, 10, 11, 12, 13}
	for i, p := range pos {
		out[p] = codeAlphabet[int(b[i])%len(codeAlphabet)]
	}
	return string(out)
}

// Rename changes a group's name and description. System groups keep their name.
func (r *Repository) Rename(ctx context.Context, groupID, name, description string) error {
	g, err := r.Get(ctx, groupID)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if g.IsSystem && name != g.Name {
		return ErrSystemGroup
	}
	if n := len([]rune(name)); n < 2 || n > 64 {
		return ErrInvalidGroupName
	}
	_, err = r.db.ExecContext(ctx, `UPDATE groups SET name = $2, description = $3 WHERE id = $1`,
		groupID, name, strings.TrimSpace(description))
	if isUniqueViolation(err, "groups_name_key") {
		return ErrGroupNameTaken
	}
	return err
}

// Delete removes a non-system group with its memberships, grants and invite codes.
func (r *Repository) Delete(ctx context.Context, groupID string) error {
	g, err := r.Get(ctx, groupID)
	if err != nil {
		return err
	}
	if g.IsSystem {
		return ErrSystemGroup
	}
	_, err = r.db.ExecContext(ctx, `DELETE FROM groups WHERE id = $1`, groupID)
	return err
}

// RemoveMember removes a user from a group. Public membership cannot be removed.
func (r *Repository) RemoveMember(ctx context.Context, groupID, userID string) error {
	if groupID == PublicGroupID {
		return ErrSystemGroup
	}
	if !ids.IsValid(groupID) || !ids.IsValid(userID) {
		return ErrGroupNotFound
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM group_members WHERE group_id = $1 AND user_id = $2`, groupID, userID)
	return err
}

// MemberIDs returns the ids of every member of a group.
func (r *Repository) MemberIDs(ctx context.Context, groupID string) ([]string, error) {
	if !ids.IsValid(groupID) {
		return nil, ErrGroupNotFound
	}
	rows, err := r.db.QueryContext(ctx, `SELECT user_id FROM group_members WHERE group_id = $1`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// SetInviteCodeActive enables or disables an invite code.
func (r *Repository) SetInviteCodeActive(ctx context.Context, code string, active bool) error {
	res, err := r.db.ExecContext(ctx, `UPDATE invite_codes SET is_active = $2 WHERE code = $1`, NormalizeCode(code), active)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrInvalidInviteCode
	}
	return nil
}
