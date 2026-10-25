package http

import (
	"fmt"
	"net/http"
	"strings"
)

func (s *Server) handleAdminCreateGroup(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	g, err := s.admin.CreateGroup(r.Context(), r.FormValue("name"), r.FormValue("description"))
	msg := ""
	if g != nil {
		msg = fmt.Sprintf("Group %s dibuat.", g.Name)
	}
	s.renderAdminConsole(w, r, actor, msg, err)
}

func (s *Server) handleAdminRenameGroup(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	err := s.admin.RenameGroup(r.Context(), r.PathValue("id"), r.FormValue("name"), r.FormValue("description"))
	s.renderAdminConsole(w, r, actor, "Group diperbarui.", err)
}

func (s *Server) handleAdminDeleteGroup(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	err := s.admin.DeleteGroup(r.Context(), r.PathValue("id"))
	s.renderAdminConsole(w, r, actor, "Group dihapus beserta keanggotaan, akses, dan kode undangannya.", err)
}

func (s *Server) handleAdminAddMember(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	err := s.admin.AddMember(r.Context(), r.PathValue("id"), strings.TrimSpace(r.FormValue("user_id")))
	s.renderAdminConsole(w, r, actor, "Anggota ditambahkan.", err)
}

func (s *Server) handleAdminRemoveMember(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	err := s.admin.RemoveMember(r.Context(), r.PathValue("id"), r.PathValue("userID"))
	s.renderAdminConsole(w, r, actor, "Anggota dikeluarkan dari group.", err)
}

func (s *Server) handleAdminSetGrant(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	level := strings.TrimSpace(r.FormValue("level"))
	err := s.admin.SetGrant(r.Context(), strings.TrimSpace(r.FormValue("group_id")), strings.TrimSpace(r.FormValue("database_id")), level)
	s.renderAdminConsole(w, r, actor, fmt.Sprintf("Akses group diperbarui menjadi %s.", level), err)
}

func (s *Server) handleAdminCreateCode(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	inv, err := s.admin.CreateInviteCode(r.Context(), actor.ID, strings.TrimSpace(r.FormValue("group_id")), r.FormValue("code"))
	msg := ""
	if inv != nil {
		msg = fmt.Sprintf("Kode undangan %s aktif.", inv.Code)
	}
	s.renderAdminConsole(w, r, actor, msg, err)
}

func (s *Server) handleAdminSetCodeActive(w http.ResponseWriter, r *http.Request) {
	actor, _, ok := s.requirePlatformAdmin(w, r)
	if !ok {
		return
	}
	active := r.FormValue("active") == "true"
	err := s.admin.SetInviteCodeActive(r.Context(), r.PathValue("code"), active)
	msg := "Kode undangan dinonaktifkan."
	if active {
		msg = "Kode undangan diaktifkan."
	}
	s.renderAdminConsole(w, r, actor, msg, err)
}
