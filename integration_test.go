package main_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
	app "review-assigner/internal/app"
	httpserver "review-assigner/internal/http"
)

type testEnv struct {
	t      *testing.T
	db     *sql.DB
	server *httptest.Server
	client *http.Client
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://reassv:reassv@localhost:5432/reasdb?sslmode=disable"
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		t.Fatalf("ping db: %v", err)
	}

	if err := resetDB(ctx, db); err != nil {
		_ = db.Close()
		t.Fatalf("reset db: %v", err)
	}

	svc := app.NewService(db)
	handler := httpserver.NewHandler(svc)
	srv := httptest.NewServer(handler)

	return &testEnv{
		t:      t,
		db:     db,
		server: srv,
		client: srv.Client(),
	}
}

func (e *testEnv) close() {
	e.server.Close()
	_ = e.db.Close()
}

func (e *testEnv) url(path string) string {
	return e.server.URL + path
}

func resetDB(ctx context.Context, db *sql.DB) error {
	schema := `
DROP TABLE IF EXISTS pull_requests;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS teams;

CREATE TABLE teams (
    team_name TEXT PRIMARY KEY
);

CREATE TABLE users (
    user_id TEXT PRIMARY KEY,
    username TEXT NOT NULL,
    team_name TEXT NOT NULL references teams(team_name),
    is_active BOOLEAN NOT NULL DEFAULT TRUE
);

CREATE TABLE pull_requests (
    pull_request_id TEXT PRIMARY KEY,
    pull_request_name TEXT NOT NULL,
    author_id TEXT NOT NULL references users(user_id),
    status TEXT NOT NULL CHECK (status IN ('OPEN', 'MERGED')),
    assigned_reviewers TEXT[] NOT NULL DEFAULT '{}' CHECK (cardinality(assigned_reviewers) <= 2),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    merged_at TIMESTAMP WITH TIME ZONE
);
`
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	return nil
}

func (e *testEnv) postJSON(path string, body any) (*http.Response, []byte) {
	e.t.Helper()

	var buf io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			e.t.Fatalf("marshal body: %v", err)
		}
		buf = bytes.NewReader(data)
	}

	req, err := http.NewRequest(http.MethodPost, e.url(path), buf)
	if err != nil {
		e.t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatalf("do request: %v", err)
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		e.t.Fatalf("read body: %v", err)
	}

	return resp, data
}

func (e *testEnv) get(path string) (*http.Response, []byte) {
	e.t.Helper()

	resp, err := e.client.Get(e.url(path))
	if err != nil {
		e.t.Fatalf("do request: %v", err)
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		e.t.Fatalf("read body: %v", err)
	}

	return resp, data
}

type teamResponse struct {
	Team app.Team `json:"team"`
}

type userResponse struct {
	User app.User `json:"user"`
}

type prResponse struct {
	PR app.PullRequest `json:"pr"`
}

type reassignResponse struct {
	PR         app.PullRequest `json:"pr"`
	ReplacedBy string          `json:"replaced_by"`
}

type userReviewsResponse struct {
	UserID       string                 `json:"user_id"`
	PullRequests []app.PullRequestShort `json:"pull_requests"`
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorResponse struct {
	Error errorBody `json:"error"`
}

func createTeam(t *testing.T, env *testEnv, name string, members []app.TeamMember) app.Team {
	t.Helper()

	req := app.Team{
		Name:    name,
		Members: members,
	}

	resp, data := env.postJSON("/team/add", req)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create team: expected 201, got %d, body=%s", resp.StatusCode, string(data))
	}

	var body teamResponse
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("unmarshal team response: %v", err)
	}
	return body.Team
}

func createPullRequest(t *testing.T, env *testEnv, id, name, authorID string) app.PullRequest {
	t.Helper()

	req := map[string]any{
		"pull_request_id":   id,
		"pull_request_name": name,
		"author_id":         authorID,
	}

	resp, data := env.postJSON("/pullRequest/create", req)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create PR: expected 201, got %d, body=%s", resp.StatusCode, string(data))
	}

	var body prResponse
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("unmarshal PR response: %v", err)
	}
	return body.PR
}

func mergePullRequest(t *testing.T, env *testEnv, id string) app.PullRequest {
	t.Helper()

	req := map[string]any{
		"pull_request_id": id,
	}

	resp, data := env.postJSON("/pullRequest/merge", req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("merge PR: expected 200, got %d, body=%s", resp.StatusCode, string(data))
	}

	var body prResponse
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("unmarshal PR response: %v", err)
	}
	return body.PR
}

func TestTeamAddAndGet(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	members := []app.TeamMember{
		{ID: "u1", Name: "Alice", IsActive: true},
		{ID: "u2", Name: "Bob", IsActive: true},
	}

	created := createTeam(t, env, "team-1", members)

	if created.Name != "team-1" {
		t.Fatalf("expected team_name %q, got %q", "team-1", created.Name)
	}
	if len(created.Members) != len(members) {
		t.Fatalf("expected %d members, got %d", len(members), len(created.Members))
	}

	resp, data := env.get("/team/get?team_name=team-1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get team: expected 200, got %d, body=%s", resp.StatusCode, string(data))
	}

	var got app.Team
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal team: %v", err)
	}

	if got.Name != "team-1" {
		t.Errorf("expected team_name %q, got %q", "team-1", got.Name)
	}
	if len(got.Members) != len(members) {
		t.Fatalf("expected %d members, got %d", len(members), len(got.Members))
	}
}

func TestTeamAdd_AlreadyExists(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	members := []app.TeamMember{
		{ID: "u1", Name: "Alice", IsActive: true},
	}

	_ = createTeam(t, env, "team-1", members)

	resp, data := env.postJSON("/team/add", app.Team{
		Name:    "team-1",
		Members: members,
	})

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for duplicate team, got %d, body=%s", resp.StatusCode, string(data))
	}

	var errResp errorResponse
	if err := json.Unmarshal(data, &errResp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if errResp.Error.Code != "TEAM_EXISTS" {
		t.Fatalf("expected error code TEAM_EXISTS, got %q", errResp.Error.Code)
	}
}

func TestTeamDeactivateMembers(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	members := []app.TeamMember{
		{ID: "u1", Name: "Alice", IsActive: true},
		{ID: "u2", Name: "Bob", IsActive: true},
		{ID: "u3", Name: "Carol", IsActive: true},
	}

	createTeam(t, env, "team-1", members)
	createPullRequest(t, env, "pr-1", "Test PR", "u1")

	req := map[string]any{
		"team_name": "team-1",
	}
	resp, data := env.postJSON("/team/deactivateMembers", req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("deactivateMembers: expected 200, got %d, body=%s", resp.StatusCode, string(data))
	}

	var body teamResponse
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("unmarshal team response: %v", err)
	}

	if body.Team.Name != "team-1" {
		t.Fatalf("expected team_name %q, got %q", "team-1", body.Team.Name)
	}
	for _, m := range body.Team.Members {
		if m.IsActive {
			t.Fatalf("expected all members to be inactive, but %s is active", m.ID)
		}
	}

	resp, data = env.get("/users/getReview?user_id=u2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("getReview: expected 200, got %d, body=%s", resp.StatusCode, string(data))
	}
	var reviews userReviewsResponse
	if err := json.Unmarshal(data, &reviews); err != nil {
		t.Fatalf("unmarshal reviews: %v", err)
	}
	if len(reviews.PullRequests) != 0 {
		t.Fatalf("expected no reviews for u2 after team deactivate, got %d", len(reviews.PullRequests))
	}
}

func TestUserSetIsActive_DeactivatesAndCleansOpenAssignments(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	members := []app.TeamMember{
		{ID: "u1", Name: "Alice", IsActive: true},
		{ID: "u2", Name: "Bob", IsActive: true},
		{ID: "u3", Name: "Carol", IsActive: true},
	}
	createTeam(t, env, "team-1", members)

	createPullRequest(t, env, "pr-open", "Open PR", "u1")
	createPullRequest(t, env, "pr-merged", "Merged PR", "u1")
	mergePullRequest(t, env, "pr-merged")

	resp, data := env.get("/users/getReview?user_id=u2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("getReview before deactivate: expected 200, got %d, body=%s", resp.StatusCode, string(data))
	}
	var before userReviewsResponse
	if err := json.Unmarshal(data, &before); err != nil {
		t.Fatalf("unmarshal before: %v", err)
	}
	if len(before.PullRequests) == 0 {
		t.Fatalf("expected some reviews for u2 before deactivate")
	}

	req := map[string]any{
		"user_id":   "u2",
		"is_active": false,
	}
	resp, data = env.postJSON("/users/setIsActive", req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setIsActive: expected 200, got %d, body=%s", resp.StatusCode, string(data))
	}
	var setResp userResponse
	if err := json.Unmarshal(data, &setResp); err != nil {
		t.Fatalf("unmarshal user: %v", err)
	}
	if setResp.User.ID != "u2" || setResp.User.IsActive {
		t.Fatalf("expected user u2 to be inactive, got %+v", setResp.User)
	}

	resp, data = env.get("/users/getReview?user_id=u2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("getReview after deactivate: expected 200, got %d, body=%s", resp.StatusCode, string(data))
	}
	var after userReviewsResponse
	if err := json.Unmarshal(data, &after); err != nil {
		t.Fatalf("unmarshal after: %v", err)
	}

	for _, pr := range after.PullRequests {
		if pr.ID == "pr-open" {
			t.Fatalf("user u2 still assigned to open PR %s", pr.ID)
		}
	}
}

func TestPullRequestCreate_AssignsReviewersFromTeam(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	members := []app.TeamMember{
		{ID: "u1", Name: "Alice", IsActive: true},
		{ID: "u2", Name: "Bob", IsActive: true},
		{ID: "u3", Name: "Carol", IsActive: true},
		{ID: "u4", Name: "Dave", IsActive: true},
	}
	createTeam(t, env, "team-1", members)

	pr := createPullRequest(t, env, "pr-1", "Test PR", "u1")

	if pr.Status != "OPEN" {
		t.Fatalf("expected status OPEN, got %q", pr.Status)
	}
	if len(pr.AssignedReviewers) != 2 {
		t.Fatalf("expected 2 reviewers, got %d: %v", len(pr.AssignedReviewers), pr.AssignedReviewers)
	}
	expected := []string{"u2", "u3"}
	for i, id := range expected {
		if pr.AssignedReviewers[i] != id {
			t.Fatalf("expected reviewer[%d]=%q, got %q", i, id, pr.AssignedReviewers[i])
		}
	}
}

func TestPullRequestMerge_SetsMergedStatus(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	members := []app.TeamMember{
		{ID: "u1", Name: "Alice", IsActive: true},
		{ID: "u2", Name: "Bob", IsActive: true},
	}
	createTeam(t, env, "team-1", members)

	createPullRequest(t, env, "pr-1", "Test PR", "u1")
	pr := mergePullRequest(t, env, "pr-1")

	if pr.Status != "MERGED" {
		t.Fatalf("expected status MERGED, got %q", pr.Status)
	}
	if pr.MergedAt == nil {
		t.Fatalf("expected non-nil merged_at")
	}
	if pr.CreatedAt == nil {
		t.Fatalf("expected non-nil created_at")
	}
}

func TestPullRequestReassign_Success(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	members := []app.TeamMember{
		{ID: "u1", Name: "Alice", IsActive: true},
		{ID: "u2", Name: "Bob", IsActive: true},
		{ID: "u3", Name: "Carol", IsActive: true},
		{ID: "u4", Name: "Dave", IsActive: true},
	}
	createTeam(t, env, "team-1", members)

	pr := createPullRequest(t, env, "pr-1", "Test PR", "u1")
	if len(pr.AssignedReviewers) != 2 {
		t.Fatalf("expected 2 reviewers, got %v", pr.AssignedReviewers)
	}

	req := map[string]any{
		"pull_request_id": "pr-1",
		"old_user_id":     "u2",
	}
	resp, data := env.postJSON("/pullRequest/reassign", req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reassign: expected 200, got %d, body=%s", resp.StatusCode, string(data))
	}

	var body reassignResponse
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("unmarshal reassign: %v", err)
	}

	if body.ReplacedBy != "u4" {
		t.Fatalf("expected replaced_by u4, got %q", body.ReplacedBy)
	}

	foundOld := false
	foundNew := false
	for _, id := range body.PR.AssignedReviewers {
		if id == "u2" {
			foundOld = true
		}
		if id == "u4" {
			foundNew = true
		}
	}
	if foundOld {
		t.Fatalf("old reviewer u2 still present in assigned_reviewers: %v", body.PR.AssignedReviewers)
	}
	if !foundNew {
		t.Fatalf("new reviewer u4 not present in assigned_reviewers: %v", body.PR.AssignedReviewers)
	}
}

func TestPullRequestReassign_NoCandidate(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	members := []app.TeamMember{
		{ID: "u1", Name: "Alice", IsActive: true},
		{ID: "u2", Name: "Bob", IsActive: true},
	}
	createTeam(t, env, "team-1", members)

	createPullRequest(t, env, "pr-1", "Test PR", "u1")

	req := map[string]any{
		"pull_request_id": "pr-1",
		"old_user_id":     "u2",
	}
	resp, data := env.postJSON("/pullRequest/reassign", req)

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 when no candidate, got %d, body=%s", resp.StatusCode, string(data))
	}
	var errResp errorResponse
	if err := json.Unmarshal(data, &errResp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if errResp.Error.Code != "NO_CANDIDATE" {
		t.Fatalf("expected error code NO_CANDIDATE, got %q", errResp.Error.Code)
	}
}

func TestStatsAssignments(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	members := []app.TeamMember{
		{ID: "u1", Name: "Alice", IsActive: true},
		{ID: "u2", Name: "Bob", IsActive: true},
		{ID: "u3", Name: "Carol", IsActive: true},
		{ID: "u4", Name: "Dave", IsActive: true},
	}
	createTeam(t, env, "team-1", members)

	createPullRequest(t, env, "pr-1", "PR 1", "u1")
	createPullRequest(t, env, "pr-2", "PR 2", "u2")

	resp, data := env.get("/stats/assignments")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stats: expected 200, got %d, body=%s", resp.StatusCode, string(data))
	}

	var stats app.AssignmentStats
	if err := json.Unmarshal(data, &stats); err != nil {
		t.Fatalf("unmarshal stats: %v", err)
	}

	byUser := map[string]int{}
	for _, u := range stats.ByUser {
		byUser[u.UserID] = u.Assignments
	}

	if byUser["u1"] != 1 {
		t.Fatalf("expected u1 assignments=1, got %d", byUser["u1"])
	}
	if byUser["u2"] != 1 {
		t.Fatalf("expected u2 assignments=1, got %d", byUser["u2"])
	}
	if byUser["u3"] != 2 {
		t.Fatalf("expected u3 assignments=2, got %d", byUser["u3"])
	}

	byPR := map[string]int{}
	for _, pr := range stats.ByPR {
		byPR[pr.PullRequestID] = pr.Assignments
	}
	if byPR["pr-1"] != 2 || byPR["pr-2"] != 2 {
		t.Fatalf("expected each PR to have 2 reviewers, got: %#v", byPR)
	}
}

func TestTeamGet_MissingName(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	resp, data := env.get("/team/get")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing team_name, got %d, body=%s", resp.StatusCode, string(data))
	}
	expected := "team_name is required\n"
	if string(data) != expected {
		t.Fatalf("expected body %q, got %q", expected, string(data))
	}
}

func TestTeamGet_NotFound(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	resp, data := env.get("/team/get?team_name=unknown")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown team, got %d, body=%s", resp.StatusCode, string(data))
	}

	var errResp errorResponse
	if err := json.Unmarshal(data, &errResp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if errResp.Error.Code != "NOT_FOUND" {
		t.Fatalf("expected error code NOT_FOUND, got %q", errResp.Error.Code)
	}
}

func TestTeamDeactivateMembers_MissingName(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	resp, data := env.postJSON("/team/deactivateMembers", map[string]any{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing team_name, got %d, body=%s", resp.StatusCode, string(data))
	}
	expected := "team_name is required\n"
	if string(data) != expected {
		t.Fatalf("expected body %q, got %q", expected, string(data))
	}
}

func TestTeamDeactivateMembers_NotFound(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	resp, data := env.postJSON("/team/deactivateMembers", map[string]any{
		"team_name": "unknown",
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown team, got %d, body=%s", resp.StatusCode, string(data))
	}

	var errResp errorResponse
	if err := json.Unmarshal(data, &errResp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if errResp.Error.Code != "NOT_FOUND" {
		t.Fatalf("expected error code NOT_FOUND, got %q", errResp.Error.Code)
	}
}

func TestUserSetIsActive_MissingUserID(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	resp, data := env.postJSON("/users/setIsActive", map[string]any{
		"is_active": false,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing user_id, got %d, body=%s", resp.StatusCode, string(data))
	}
	expected := "user_id is required\n"
	if string(data) != expected {
		t.Fatalf("expected body %q, got %q", expected, string(data))
	}
}

func TestUserSetIsActive_UserNotFound(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	resp, data := env.postJSON("/users/setIsActive", map[string]any{
		"user_id":   "unknown",
		"is_active": false,
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown user, got %d, body=%s", resp.StatusCode, string(data))
	}

	var errResp errorResponse
	if err := json.Unmarshal(data, &errResp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if errResp.Error.Code != "NOT_FOUND" {
		t.Fatalf("expected error code NOT_FOUND, got %q", errResp.Error.Code)
	}
}

func TestUserGetReview_MissingUserID(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	resp, data := env.get("/users/getReview")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing user_id, got %d, body=%s", resp.StatusCode, string(data))
	}
	expected := "user_id is required\n"
	if string(data) != expected {
		t.Fatalf("expected body %q, got %q", expected, string(data))
	}
}

func TestPullRequestCreate_MissingFields(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	resp, data := env.postJSON("/pullRequest/create", map[string]any{
		"pull_request_name": "Test PR",
		"author_id":         "u1",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing pull_request_id, got %d, body=%s", resp.StatusCode, string(data))
	}
	if got := string(data); got != "pull_request_id is required\n" {
		t.Fatalf("expected body %q, got %q", "pull_request_id is required\n", got)
	}

	resp, data = env.postJSON("/pullRequest/create", map[string]any{
		"pull_request_id": "pr-1",
		"author_id":       "u1",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing pull_request_name, got %d, body=%s", resp.StatusCode, string(data))
	}
	if got := string(data); got != "pull_request_name is required\n" {
		t.Fatalf("expected body %q, got %q", "pull_request_name is required\n", got)
	}

	resp, data = env.postJSON("/pullRequest/create", map[string]any{
		"pull_request_id":   "pr-1",
		"pull_request_name": "Test PR",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing author_id, got %d, body=%s", resp.StatusCode, string(data))
	}
	if got := string(data); got != "author_id is required\n" {
		t.Fatalf("expected body %q, got %q", "author_id is required\n", got)
	}
}

func TestPullRequestCreate_AuthorNotFound(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	resp, data := env.postJSON("/pullRequest/create", map[string]any{
		"pull_request_id":   "pr-1",
		"pull_request_name": "Test PR",
		"author_id":         "unknown",
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 when author not found, got %d, body=%s", resp.StatusCode, string(data))
	}

	var errResp errorResponse
	if err := json.Unmarshal(data, &errResp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if errResp.Error.Code != "NOT_FOUND" {
		t.Fatalf("expected error code NOT_FOUND, got %q", errResp.Error.Code)
	}
}

func TestPullRequestCreate_PRExists(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	members := []app.TeamMember{
		{ID: "u1", Name: "Alice", IsActive: true},
	}
	createTeam(t, env, "team-1", members)

	_ = createPullRequest(t, env, "pr-1", "Test PR", "u1")

	resp, data := env.postJSON("/pullRequest/create", map[string]any{
		"pull_request_id":   "pr-1",
		"pull_request_name": "Test PR",
		"author_id":         "u1",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 for duplicate PR, got %d, body=%s", resp.StatusCode, string(data))
	}

	var errResp errorResponse
	if err := json.Unmarshal(data, &errResp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if errResp.Error.Code != "PR_EXISTS" {
		t.Fatalf("expected error code PR_EXISTS, got %q", errResp.Error.Code)
	}
}

func TestPullRequestMerge_MissingID(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	resp, data := env.postJSON("/pullRequest/merge", map[string]any{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing pull_request_id, got %d, body=%s", resp.StatusCode, string(data))
	}
	expected := "pull_request_id is required\n"
	if string(data) != expected {
		t.Fatalf("expected body %q, got %q", expected, string(data))
	}
}

func TestPullRequestMerge_NotFound(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	resp, data := env.postJSON("/pullRequest/merge", map[string]any{
		"pull_request_id": "pr-unknown",
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown pull request, got %d, body=%s", resp.StatusCode, string(data))
	}

	var errResp errorResponse
	if err := json.Unmarshal(data, &errResp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if errResp.Error.Code != "NOT_FOUND" {
		t.Fatalf("expected error code NOT_FOUND, got %q", errResp.Error.Code)
	}
}

func TestPullRequestReassign_MissingFields(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	resp, data := env.postJSON("/pullRequest/reassign", map[string]any{
		"old_user_id": "u1",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing pull_request_id, got %d, body=%s", resp.StatusCode, string(data))
	}
	if got := string(data); got != "pull_request_id is required\n" {
		t.Fatalf("expected body %q, got %q", "pull_request_id is required\n", got)
	}

	resp, data = env.postJSON("/pullRequest/reassign", map[string]any{
		"pull_request_id": "pr-1",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing old_user_id, got %d, body=%s", resp.StatusCode, string(data))
	}
	if got := string(data); got != "old_user_id is required\n" {
		t.Fatalf("expected body %q, got %q", "old_user_id is required\n", got)
	}
}

func TestPullRequestReassign_PRNotFound(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	resp, data := env.postJSON("/pullRequest/reassign", map[string]any{
		"pull_request_id": "pr-unknown",
		"old_user_id":     "u1",
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown PR, got %d, body=%s", resp.StatusCode, string(data))
	}

	var errResp errorResponse
	if err := json.Unmarshal(data, &errResp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if errResp.Error.Code != "NOT_FOUND" {
		t.Fatalf("expected error code NOT_FOUND, got %q", errResp.Error.Code)
	}
}

func TestPullRequestReassign_MergedPR(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	members := []app.TeamMember{
		{ID: "u1", Name: "Alice", IsActive: true},
		{ID: "u2", Name: "Bob", IsActive: true},
		{ID: "u3", Name: "Carol", IsActive: true},
		{ID: "u4", Name: "Dave", IsActive: true},
	}
	createTeam(t, env, "team-1", members)

	_ = createPullRequest(t, env, "pr-1", "Test PR", "u1")
	_ = mergePullRequest(t, env, "pr-1")

	resp, data := env.postJSON("/pullRequest/reassign", map[string]any{
		"pull_request_id": "pr-1",
		"old_user_id":     "u2",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 for reassign on merged PR, got %d, body=%s", resp.StatusCode, string(data))
	}

	var errResp errorResponse
	if err := json.Unmarshal(data, &errResp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if errResp.Error.Code != "PR_MERGED" {
		t.Fatalf("expected error code PR_MERGED, got %q", errResp.Error.Code)
	}
}

func TestPullRequestReassign_UserNotAssigned(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	members := []app.TeamMember{
		{ID: "u1", Name: "Alice", IsActive: true},
		{ID: "u2", Name: "Bob", IsActive: true},
		{ID: "u3", Name: "Carol", IsActive: true},
		{ID: "u4", Name: "Dave", IsActive: true},
	}
	createTeam(t, env, "team-1", members)

	pr := createPullRequest(t, env, "pr-1", "Test PR", "u1")

	if len(pr.AssignedReviewers) != 2 {
		t.Fatalf("expected 2 reviewers, got %v", pr.AssignedReviewers)
	}

	resp, data := env.postJSON("/pullRequest/reassign", map[string]any{
		"pull_request_id": "pr-1",
		"old_user_id":     "u4",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 when user not assigned, got %d, body=%s", resp.StatusCode, string(data))
	}

	var errResp errorResponse
	if err := json.Unmarshal(data, &errResp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if errResp.Error.Code != "NOT_ASSIGNED" {
		t.Fatalf("expected error code NOT_ASSIGNED, got %q", errResp.Error.Code)
	}
}

func TestStatsAssignments_Empty(t *testing.T) {
	env := newTestEnv(t)
	defer env.close()

	resp, data := env.get("/stats/assignments")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stats: expected 200, got %d, body=%s", resp.StatusCode, string(data))
	}

	var stats app.AssignmentStats
	if err := json.Unmarshal(data, &stats); err != nil {
		t.Fatalf("unmarshal stats: %v", err)
	}

	if len(stats.ByUser) != 0 {
		t.Fatalf("expected empty ByUser stats, got %#v", stats.ByUser)
	}
	if len(stats.ByPR) != 0 {
		t.Fatalf("expected empty ByPR stats, got %#v", stats.ByPR)
	}
}
