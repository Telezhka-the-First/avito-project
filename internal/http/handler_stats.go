package httpserver

import (
	"net/http"
)

func (h *Handler) handleStatsAssignments(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	stats, err := h.service.GetAssignmentStats(r.Context())
	if err != nil {
		h.writeAppError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, stats)
}
