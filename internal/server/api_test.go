package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupAPITest(t *testing.T) {
	t.Helper()
	// Point PR storage to a temp dir so tests don't touch real data.
	dataDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataDir)

	// Set server start time so uptime is calculable.
	serverStartTime = time.Now()
}

func TestHandleStatus(t *testing.T) {
	setupAPITest(t)

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()
	handleStatus(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp StatusResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "running", resp.Status)
	assert.NotEmpty(t, resp.Uptime)
	assert.Equal(t, 0, resp.PRCount)
}

func TestHandleListPRs_Empty(t *testing.T) {
	setupAPITest(t)

	req := httptest.NewRequest(http.MethodGet, "/prs", nil)
	rec := httptest.NewRecorder()
	handleListPRs(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var prs []json.RawMessage
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&prs))
	assert.Empty(t, prs)
}

func TestHandleAddPR(t *testing.T) {
	setupAPITest(t)

	body := `{"url":"https://dev.azure.com/org/project/_git/repo/pullrequest/123","id":"123","title":"Test PR","provider":"ado","repo":"my-repo","branch":"feature/test","target":"main"}`
	req := httptest.NewRequest(http.MethodPost, "/prs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handleAddPR(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)

	var pr PRDocument
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&pr))
	assert.Equal(t, "123", pr.ID)
	assert.Equal(t, "Test PR", pr.Title)
	assert.Equal(t, "ado", pr.Provider)
	assert.Equal(t, "watching", pr.Status)

	// Verify it's persisted — list should return it now.
	listReq := httptest.NewRequest(http.MethodGet, "/prs", nil)
	listRec := httptest.NewRecorder()
	handleListPRs(listRec, listReq)
	assert.Equal(t, http.StatusOK, listRec.Code)

	var prs []*PRDocument
	require.NoError(t, json.NewDecoder(listRec.Body).Decode(&prs))
	require.Len(t, prs, 1)
	assert.Equal(t, "123", prs[0].ID)
}

func TestHandleDeletePR(t *testing.T) {
	setupAPITest(t)

	// First, create a PR.
	body := `{"url":"https://github.com/org/repo/pull/456","id":"456","title":"Delete Me","provider":"github","repo":"org/repo","branch":"fix/bug","target":"main"}`
	addReq := httptest.NewRequest(http.MethodPost, "/prs", strings.NewReader(body))
	addReq.Header.Set("Content-Type", "application/json")
	addRec := httptest.NewRecorder()
	handleAddPR(addRec, addReq)
	require.Equal(t, http.StatusCreated, addRec.Code)

	// Use the mux so path values are parsed correctly.
	mux := http.NewServeMux()
	registerRoutes(mux)

	// Delete via the mux.
	delReq := httptest.NewRequest(http.MethodDelete, "/prs/456", nil)
	delRec := httptest.NewRecorder()
	mux.ServeHTTP(delRec, delReq)
	assert.Equal(t, http.StatusNoContent, delRec.Code)

	// Verify it's gone.
	listReq := httptest.NewRequest(http.MethodGet, "/prs", nil)
	listRec := httptest.NewRecorder()
	handleListPRs(listRec, listReq)
	var prs []*PRDocument
	require.NoError(t, json.NewDecoder(listRec.Body).Decode(&prs))
	assert.Empty(t, prs)
}

func TestHandleAddPR_InvalidJSON(t *testing.T) {
	setupAPITest(t)

	req := httptest.NewRequest(http.MethodPost, "/prs", strings.NewReader("{bad json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handleAddPR(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
