package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// StatusResponse is the JSON response for GET /status.
type StatusResponse struct {
	Status  string `json:"status"`
	Uptime  string `json:"uptime"`
	PRCount int    `json:"pr_count"`
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	prs, err := ListPRs()
	count := 0
	if err == nil {
		count = len(prs)
	}

	resp := StatusResponse{
		Status:  "running",
		Uptime:  time.Since(serverStartTime).Round(time.Second).String(),
		PRCount: count,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func handleListPRs(w http.ResponseWriter, r *http.Request) {
	prs, err := ListPRs()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if prs == nil {
		prs = []*PRDocument{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(prs)
}

// AddPRRequest is the JSON body for POST /prs.
type AddPRRequest struct {
	URL            string `json:"url"`
	ID             string `json:"id"`
	Title          string `json:"title"`
	Provider       string `json:"provider"`
	Repo           string `json:"repo"`
	Branch         string `json:"branch"`
	Target         string `json:"target"`
	MaxFixAttempts int    `json:"max_fix_attempts"`
	Description    string `json:"description"`
}

func handleAddPR(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB limit
	var req AddPRRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.URL == "" || req.ID == "" || req.Provider == "" {
		http.Error(w, "url, id, and provider are required", http.StatusBadRequest)
		return
	}

	maxAttempts := req.MaxFixAttempts
	if maxAttempts <= 0 {
		maxAttempts = 5
	}

	pr := &PRDocument{
		ID:             req.ID,
		Title:          req.Title,
		Provider:       req.Provider,
		Repo:           req.Repo,
		Branch:         req.Branch,
		Target:         req.Target,
		Status:         "watching",
		URL:            req.URL,
		Created:        time.Now().UTC().Format(time.RFC3339),
		LastChecked:    time.Now().UTC().Format(time.RFC3339),
		MaxFixAttempts: maxAttempts,
		Body:           fmt.Sprintf("# %s\n\n%s\n", req.Title, req.Description),
	}

	if err := SavePR(pr); err != nil {
		slog.Error("failed to save PR", "error", err)
		http.Error(w, "failed to save PR", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(pr)
}

func handleDeletePR(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "PR ID required", http.StatusBadRequest)
		return
	}

	pr, err := FindPR(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	if err := DeletePR(pr.Provider, pr.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func handleFixPR(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "PR ID required", http.StatusBadRequest)
		return
	}

	pr, err := FindPR(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	// Respond immediately — fix runs asynchronously.
	// The caller polls PR status to track progress.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "accepted",
		"pr_id":  pr.ID,
	})

	// TODO: Queue the fix for async execution by the monitoring loop (Phase 8.3).
	slog.Info("fix requested via API", "prID", pr.ID)
}
