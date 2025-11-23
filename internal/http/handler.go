package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"review-assigner/internal/app"
)

// Handler routes HTTP requests to the application service.
type Handler struct {
	service *app.Service
}

// NewHandler creates a new HTTP handler for the provided service.
func NewHandler(service *app.Service) http.Handler {
	h := &Handler{service: service}
	mux := http.NewServeMux()
	mux.HandleFunc("/team/add", h.handleTeamAdd)
	mux.HandleFunc("/team/get", h.handleTeamGet)
	mux.HandleFunc("/team/deactivateMembers", h.handleTeamDeactivateMembers)
	mux.HandleFunc("/users/setIsActive", h.handleUserSetIsActive)
	mux.HandleFunc("/users/getReview", h.handleUserGetReview)
	mux.HandleFunc("/pullRequest/create", h.handlePullRequestCreate)
	mux.HandleFunc("/pullRequest/merge", h.handlePullRequestMerge)
	mux.HandleFunc("/pullRequest/reassign", h.handlePullRequestReassign)
	mux.HandleFunc("/stats/assignments", h.handleStatsAssignments)
	return mux
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorResponse struct {
	Error errorBody `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (h *Handler) writeAppError(w http.ResponseWriter, err error) {
	var appErr *app.Error
	if errors.As(err, &appErr) {
		status := http.StatusInternalServerError
		switch appErr.Code {
		case app.ErrorCodeTeamExists:
			status = http.StatusBadRequest
		case app.ErrorCodePRExists, app.ErrorCodePRMerged, app.ErrorCodeNoCandidate, app.ErrorCodeNotAssigned:
			status = http.StatusConflict
		case app.ErrorCodeNotFound:
			status = http.StatusNotFound
		}
		writeJSON(w, status, errorResponse{
			Error: errorBody{
				Code:    string(appErr.Code),
				Message: appErr.Message,
			},
		})
		return
	}

	http.Error(w, "internal error", http.StatusInternalServerError)
}
