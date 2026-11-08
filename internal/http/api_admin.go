package http

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"dbplatform/internal/admin"
	"dbplatform/internal/authn"
	"dbplatform/internal/dbmanager"
	"dbplatform/internal/groups"
	"dbplatform/internal/rbac"
)

func (s *Server) handleAPIAdminOverview(w http.ResponseWriter, r *http.Request) {
	_, _, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	ov, err := s.admin.Overview(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Gagal memuat data admin")
		return
	}
	writeJSON(w, http.StatusOK, ov)
}

type apiAdminCreateUserReq struct {
	Username string   `json:"username"`
	Email    string   `json:"email"`
	Password string   `json:"password"`
	Role     string   `json:"role"`
	GroupIDs []string `json:"group_ids"`
}

func (s *Server) handleAPIAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	_, _, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	var req apiAdminCreateUserReq
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		_ = readJSONBody(r, &req)
	} else {
		_ = r.ParseForm()
		req.Username = r.FormValue("username")
		req.Email = r.FormValue("email")
		req.Password = r.FormValue("password")
		req.Role = r.FormValue("role")
		req.GroupIDs = r.PostForm["group_ids"]
	}

	in := authn.RegisterInput{
		Username: req.Username,
		Email:    req.Email,
		Password: req.Password,
		Role:     strings.TrimSpace(req.Role),
	}
	u, err := s.admin.CreateUser(r.Context(), in, req.GroupIDs)
	if err != nil {
		msg, code := adminErrorMessage(err)
		writeJSON(w, code, map[string]any{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"message": fmt.Sprintf("User %s berhasil dibuat.", u.Username),
		"user":    u,
	})
}

func (s *Server) handleAPIAdminSetUserRole(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	var body struct {
		Role string `json:"role"`
	}
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		_ = readJSONBody(r, &body)
	} else {
		body.Role = r.FormValue("role")
	}
	role := strings.TrimSpace(body.Role)
	err := s.admin.SetUserRole(r.Context(), actor.ID, r.PathValue("id"), role)
	if err != nil {
		msg, code := adminErrorMessage(err)
		writeJSON(w, code, map[string]any{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"message": fmt.Sprintf("Role diperbarui menjadi %s.", role),
	})
}

func (s *Server) handleAPIAdminSetUserActive(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	var body struct {
		Active any `json:"active"`
	}
	active := false
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		_ = readJSONBody(r, &body)
		if b, ok := body.Active.(bool); ok {
			active = b
		} else if str, ok := body.Active.(string); ok {
			active = str == "true"
		}
	} else {
		active = r.FormValue("active") == "true"
	}
	err := s.admin.SetUserActive(r.Context(), actor.ID, r.PathValue("id"), active)
	if err != nil {
		msg, code := adminErrorMessage(err)
		writeJSON(w, code, map[string]any{"error": msg})
		return
	}
	msg := "Akun dinonaktifkan dan seluruh sesinya dicabut."
	if active {
		msg = "Akun diaktifkan kembali."
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": msg})
}

func (s *Server) handleAPIAdminResetPassword(w http.ResponseWriter, r *http.Request) {
	_, _, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		_ = readJSONBody(r, &body)
	} else {
		body.Password = r.FormValue("password")
	}
	err := s.admin.ResetPassword(r.Context(), r.PathValue("id"), body.Password)
	if err != nil {
		msg, code := adminErrorMessage(err)
		writeJSON(w, code, map[string]any{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"message": "Password direset. User harus login ulang dengan password baru.",
	})
}

func (s *Server) handleAPIAdminCreateGroup(w http.ResponseWriter, r *http.Request) {
	_, _, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		_ = readJSONBody(r, &body)
	} else {
		body.Name = r.FormValue("name")
		body.Description = r.FormValue("description")
	}
	g, err := s.admin.CreateGroup(r.Context(), body.Name, body.Description)
	if err != nil {
		msg, code := adminErrorMessage(err)
		writeJSON(w, code, map[string]any{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"message": fmt.Sprintf("Group %s dibuat.", g.Name),
		"group":   g,
	})
}

func (s *Server) handleAPIAdminRenameGroup(w http.ResponseWriter, r *http.Request) {
	_, _, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		_ = readJSONBody(r, &body)
	} else {
		body.Name = r.FormValue("name")
		body.Description = r.FormValue("description")
	}
	err := s.admin.RenameGroup(r.Context(), r.PathValue("id"), body.Name, body.Description)
	if err != nil {
		msg, code := adminErrorMessage(err)
		writeJSON(w, code, map[string]any{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "Group diperbarui."})
}

func (s *Server) handleAPIAdminDeleteGroup(w http.ResponseWriter, r *http.Request) {
	_, _, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	err := s.admin.DeleteGroup(r.Context(), r.PathValue("id"))
	if err != nil {
		msg, code := adminErrorMessage(err)
		writeJSON(w, code, map[string]any{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "Group dihapus beserta keanggotaan, akses, dan kode undangannya."})
}

func (s *Server) handleAPIAdminAddMember(w http.ResponseWriter, r *http.Request) {
	_, _, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	var body struct {
		UserID string `json:"user_id"`
	}
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		_ = readJSONBody(r, &body)
	} else {
		body.UserID = r.FormValue("user_id")
	}
	err := s.admin.AddMember(r.Context(), r.PathValue("id"), strings.TrimSpace(body.UserID))
	if err != nil {
		msg, code := adminErrorMessage(err)
		writeJSON(w, code, map[string]any{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "Anggota ditambahkan."})
}

func (s *Server) handleAPIAdminRemoveMember(w http.ResponseWriter, r *http.Request) {
	_, _, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	err := s.admin.RemoveMember(r.Context(), r.PathValue("id"), r.PathValue("userID"))
	if err != nil {
		msg, code := adminErrorMessage(err)
		writeJSON(w, code, map[string]any{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": "Anggota dikeluarkan dari group."})
}

func (s *Server) handleAPIAdminSetGrant(w http.ResponseWriter, r *http.Request) {
	_, _, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	var body struct {
		GroupID    string `json:"group_id"`
		DatabaseID string `json:"database_id"`
		Level      string `json:"level"`
	}
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		_ = readJSONBody(r, &body)
	} else {
		body.GroupID = r.FormValue("group_id")
		body.DatabaseID = r.FormValue("database_id")
		body.Level = r.FormValue("level")
	}
	level := strings.TrimSpace(body.Level)
	err := s.admin.SetGrant(r.Context(), strings.TrimSpace(body.GroupID), strings.TrimSpace(body.DatabaseID), level)
	if err != nil {
		msg, code := adminErrorMessage(err)
		writeJSON(w, code, map[string]any{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"message": fmt.Sprintf("Akses group diperbarui menjadi %s.", level),
	})
}

func (s *Server) handleAPIAdminCreateTarget(w http.ResponseWriter, r *http.Request) {
	_, _, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	var in dbmanager.TargetInput
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		_ = readJSONBody(r, &in)
	} else {
		in = targetInputFromForm(r)
	}
	t, err := s.admin.CreateTarget(r.Context(), in)
	if err != nil {
		msg, code := adminErrorMessage(err)
		writeJSON(w, code, map[string]any{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"message": targetResultMessage(t, "didaftarkan"),
		"target":  t,
	})
}

func (s *Server) handleAPIAdminUpdateTarget(w http.ResponseWriter, r *http.Request) {
	_, _, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	var in dbmanager.TargetInput
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		_ = readJSONBody(r, &in)
	} else {
		in = targetInputFromForm(r)
	}
	t, err := s.admin.UpdateTarget(r.Context(), r.PathValue("id"), in)
	if err != nil {
		msg, code := adminErrorMessage(err)
		writeJSON(w, code, map[string]any{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"message": targetResultMessage(t, "diperbarui"),
		"target":  t,
	})
}

func (s *Server) handleAPIAdminTestTarget(w http.ResponseWriter, r *http.Request) {
	_, _, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	t, err := s.admin.TestTarget(r.Context(), r.PathValue("id"))
	if err != nil {
		msg, code := adminErrorMessage(err)
		writeJSON(w, code, map[string]any{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"message": targetResultMessage(t, "diuji"),
		"target":  t,
	})
}

func (s *Server) handleAPIAdminDeleteTarget(w http.ResponseWriter, r *http.Request) {
	_, _, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	err := s.admin.DeleteTarget(r.Context(), r.PathValue("id"))
	if err != nil {
		msg, code := adminErrorMessage(err)
		writeJSON(w, code, map[string]any{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"message": "Database dihapus dari platform beserta seluruh akses group-nya.",
	})
}

func (s *Server) handleAPIAdminCreateCode(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	var body struct {
		GroupID string `json:"group_id"`
		Code    string `json:"code"`
	}
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		_ = readJSONBody(r, &body)
	} else {
		body.GroupID = r.FormValue("group_id")
		body.Code = r.FormValue("code")
	}
	inv, err := s.admin.CreateInviteCode(r.Context(), actor.ID, strings.TrimSpace(body.GroupID), body.Code)
	if err != nil {
		msg, code := adminErrorMessage(err)
		writeJSON(w, code, map[string]any{"error": msg})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"message": fmt.Sprintf("Kode undangan %s aktif.", inv.Code),
		"code":    inv,
	})
}

func (s *Server) handleAPIAdminSetCodeActive(w http.ResponseWriter, r *http.Request) {
	_, _, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	var body struct {
		Active any `json:"active"`
	}
	active := false
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		_ = readJSONBody(r, &body)
		if b, ok := body.Active.(bool); ok {
			active = b
		} else if str, ok := body.Active.(string); ok {
			active = str == "true"
		}
	} else {
		active = r.FormValue("active") == "true"
	}
	err := s.admin.SetInviteCodeActive(r.Context(), r.PathValue("code"), active)
	if err != nil {
		msg, code := adminErrorMessage(err)
		writeJSON(w, code, map[string]any{"error": msg})
		return
	}
	msg := "Kode undangan dinonaktifkan."
	if active {
		msg = "Kode undangan diaktifkan."
	}
	writeJSON(w, http.StatusOK, map[string]any{"message": msg})
}

func adminErrorMessage(err error) (string, int) {
	var ve *dbmanager.ValidationError
	if errors.As(err, &ve) {
		return "Data koneksi tidak valid: " + ve.Message, http.StatusUnprocessableEntity
	}
	known := []error{
		admin.ErrSelfLockout, admin.ErrInvalidLevel,
		authn.ErrInvalidUsername, authn.ErrInvalidEmail, authn.ErrWeakPassword, authn.ErrUsernameTaken,
		authn.ErrEmailTaken, authn.ErrInvalidRole, authn.ErrUserNotFound,
		groups.ErrGroupNotFound, groups.ErrGroupNameTaken, groups.ErrInvalidGroupName, groups.ErrInvalidInviteCode,
		groups.ErrInviteCodeTaken, groups.ErrSystemGroup,
		rbac.ErrUnknownGrantTarget,
		dbmanager.ErrTargetNotFound, dbmanager.ErrTargetNameTaken,
	}
	for _, k := range known {
		if errors.Is(err, k) {
			return k.Error(), http.StatusUnprocessableEntity
		}
	}
	return "Terjadi kesalahan pada server. Silakan coba lagi.", http.StatusInternalServerError
}

func targetInputFromForm(r *http.Request) dbmanager.TargetInput {
	port, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("port")))
	return dbmanager.TargetInput{
		Name:     r.FormValue("name"),
		Host:     r.FormValue("host"),
		Port:     port,
		DBName:   r.FormValue("db_name"),
		Username: r.FormValue("username"),
		Password: r.FormValue("password"),
		SSLMode:  r.FormValue("ssl_mode"),
	}
}

func targetResultMessage(t *dbmanager.Target, action string) string {
	if t == nil {
		return ""
	}
	if t.Status == dbmanager.StatusConnected {
		return fmt.Sprintf("Database %s %s dan berhasil terhubung.", t.Name, action)
	}
	return fmt.Sprintf("Database %s %s, tetapi koneksi gagal: %s", t.Name, action, t.LastError)
}
