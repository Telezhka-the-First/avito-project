package httpserver

import (
	"encoding/json"
	"net/http"
	"review-assigner/internal/app"
)

func (h *Handler) handleTeamAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	defer func() {
		_ = r.Body.Close()
	}()

	var req app.Team
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	team, err := h.service.CreateTeam(r.Context(), req)
	if err != nil {
		h.writeAppError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"team": team,
	})
}

func (h *Handler) handleTeamGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	name := r.URL.Query().Get("team_name")
	if name == "" {
		http.Error(w, "team_name is required", http.StatusBadRequest)
		return
	}

	team, err := h.service.GetTeam(r.Context(), name)
	if err != nil {
		h.writeAppError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, team)
}

type teamDeactivateMembersRequest struct {
	TeamName string `json:"team_name"`
}

func (h *Handler) handleTeamDeactivateMembers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	defer func() {
		_ = r.Body.Close()
	}()

	var req teamDeactivateMembersRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if req.TeamName == "" {
		http.Error(w, "team_name is required", http.StatusBadRequest)
		return
	}

	team, err := h.service.DeactivateTeamMembers(r.Context(), req.TeamName)
	if err != nil {
		h.writeAppError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"team": team,
	})
}
