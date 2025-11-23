package httpserver

import (
	"encoding/json"
	"net/http"
)

type createPullRequestRequest struct {
	ID       string `json:"pull_request_id"`
	Name     string `json:"pull_request_name"`
	AuthorID string `json:"author_id"`
}

type mergePullRequestRequest struct {
	ID string `json:"pull_request_id"`
}

type reassignPullRequestRequest struct {
	ID        string `json:"pull_request_id"`
	OldUserID string `json:"old_user_id"`
}

func (h *Handler) handlePullRequestCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	defer func() {
		_ = r.Body.Close()
	}()

	var req createPullRequestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if req.ID == "" {
		http.Error(w, "pull_request_id is required", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "pull_request_name is required", http.StatusBadRequest)
		return
	}
	if req.AuthorID == "" {
		http.Error(w, "author_id is required", http.StatusBadRequest)
		return
	}

	pr, err := h.service.CreatePullRequest(r.Context(), req.ID, req.Name, req.AuthorID)
	if err != nil {
		h.writeAppError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"pr": pr,
	})
}

func (h *Handler) handlePullRequestMerge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	defer func() {
		_ = r.Body.Close()
	}()

	var req mergePullRequestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if req.ID == "" {
		http.Error(w, "pull_request_id is required", http.StatusBadRequest)
		return
	}

	pr, err := h.service.MergePullRequest(r.Context(), req.ID)
	if err != nil {
		h.writeAppError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"pr": pr,
	})
}

func (h *Handler) handlePullRequestReassign(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	defer func() {
		_ = r.Body.Close()
	}()

	var req reassignPullRequestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if req.ID == "" {
		http.Error(w, "pull_request_id is required", http.StatusBadRequest)
		return
	}
	if req.OldUserID == "" {
		http.Error(w, "old_user_id is required", http.StatusBadRequest)
		return
	}

	pr, replacedBy, err := h.service.ReassignReviewer(r.Context(), req.ID, req.OldUserID)
	if err != nil {
		h.writeAppError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"pr":          pr,
		"replaced_by": replacedBy,
	})
}
