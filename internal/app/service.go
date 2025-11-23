package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// Service provides application business operations backed by a SQL database.
type Service struct {
	db *sql.DB
}

// NewService creates a new Service using the provided database handle.
func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

// CreateTeam creates a new team and upserts its members in the database.
func (s *Service) CreateTeam(ctx context.Context, team Team) (Team, error) {
	const selectTeamQuery = `SELECT team_name FROM teams WHERE team_name = $1`
	var existing string
	err := s.db.QueryRowContext(ctx, selectTeamQuery, team.Name).Scan(&existing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Team{}, fmt.Errorf("check team: %w", err)
	}
	if err == nil {
		return Team{}, &Error{Code: ErrorCodeTeamExists, Message: "team_name already exists"}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Team{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	const insertTeamQuery = `INSERT INTO teams(team_name) VALUES ($1)`
	if _, err := tx.ExecContext(ctx, insertTeamQuery, team.Name); err != nil {
		return Team{}, fmt.Errorf("insert team: %w", err)
	}

	const upsertUserQuery = `
INSERT INTO users(user_id, username, team_name, is_active)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id) DO UPDATE
SET username = EXCLUDED.username,
    team_name = EXCLUDED.team_name,
    is_active = EXCLUDED.is_active
`
	for _, m := range team.Members {
		if _, err := tx.ExecContext(ctx, upsertUserQuery, m.ID, m.Name, team.Name, m.IsActive); err != nil {
			return Team{}, fmt.Errorf("upsert user %s: %w", m.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return Team{}, fmt.Errorf("commit tx: %w", err)
	}

	return team, nil
}

// GetTeam returns a team and its members by team name.
func (s *Service) GetTeam(ctx context.Context, name string) (Team, error) {
	const selectTeamQuery = `SELECT team_name FROM teams WHERE team_name = $1`
	var teamName string
	err := s.db.QueryRowContext(ctx, selectTeamQuery, name).Scan(&teamName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Team{}, &Error{Code: ErrorCodeNotFound, Message: "team not found"}
		}
		return Team{}, fmt.Errorf("get team: %w", err)
	}

	const selectMembersQuery = `SELECT user_id, username, is_active FROM users WHERE team_name = $1 ORDER BY user_id`
	rows, err := s.db.QueryContext(ctx, selectMembersQuery, name)
	if err != nil {
		return Team{}, fmt.Errorf("get team members: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var members []TeamMember
	for rows.Next() {
		var m TeamMember
		if err := rows.Scan(&m.ID, &m.Name, &m.IsActive); err != nil {
			return Team{}, fmt.Errorf("scan member: %w", err)
		}
		members = append(members, m)
	}

	if err = rows.Err(); err != nil {
		return Team{}, fmt.Errorf("members rows: %w", err)
	}

	return Team{
		Name:    name,
		Members: members,
	}, nil
}

// CreatePullRequest creates a new pull request and assigns initial reviewers.
func (s *Service) CreatePullRequest(ctx context.Context, id, name, authorID string) (PullRequest, error) {
	const selectPRQuery = `SELECT pull_request_id FROM pull_requests WHERE pull_request_id = $1`
	var existing string
	err := s.db.QueryRowContext(ctx, selectPRQuery, id).Scan(&existing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return PullRequest{}, fmt.Errorf("check pull request: %w", err)
	}
	if err == nil {
		return PullRequest{}, &Error{Code: ErrorCodePRExists, Message: "PR id already exists"}
	}

	const selectAuthorTeamQuery = `SELECT team_name FROM users WHERE user_id = $1`
	var teamName string
	err = s.db.QueryRowContext(ctx, selectAuthorTeamQuery, authorID).Scan(&teamName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PullRequest{}, &Error{Code: ErrorCodeNotFound, Message: "author or team not found"}
		}
		return PullRequest{}, fmt.Errorf("get author team: %w", err)
	}

	const selectReviewersQuery = `
SELECT user_id
FROM users
WHERE team_name = $1
  AND user_id <> $2
  AND is_active = TRUE
ORDER BY user_id
`
	rows, err := s.db.QueryContext(ctx, selectReviewersQuery, teamName, authorID)
	if err != nil {
		return PullRequest{}, fmt.Errorf("select reviewers: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var reviewers []string
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			return PullRequest{}, fmt.Errorf("scan reviewer: %w", err)
		}
		reviewers = append(reviewers, uid)
		if len(reviewers) == 2 {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return PullRequest{}, fmt.Errorf("scan reviewers: %w", err)
	}

	assigned := reviewers
	if assigned == nil {
		assigned = []string{}
	}

	const insertPRQuery = `
INSERT INTO pull_requests(pull_request_id, pull_request_name, author_id, status, assigned_reviewers)
VALUES ($1, $2, $3, 'OPEN', $4)
RETURNING pull_request_id, pull_request_name, author_id, status, assigned_reviewers, created_at, merged_at
`
	var pr PullRequest
	var createdAt time.Time
	var mergedAt sql.NullTime
	err = s.db.QueryRowContext(ctx, insertPRQuery, id, name, authorID, pq.Array(assigned)).
		Scan(&pr.ID, &pr.Name, &pr.AuthorID, &pr.Status, pq.Array(&pr.AssignedReviewers), &createdAt, &mergedAt)
	if err != nil {
		return PullRequest{}, fmt.Errorf("insert pull request: %w", err)
	}

	pr.CreatedAt = &createdAt
	if mergedAt.Valid {
		t := mergedAt.Time
		pr.MergedAt = &t
	}

	return pr, nil
}

// MergePullRequest marks a pull request as merged.
func (s *Service) MergePullRequest(ctx context.Context, prID string) (PullRequest, error) {
	const query = `
UPDATE pull_requests
SET status = 'MERGED',
    merged_at = COALESCE(merged_at, NOW())
WHERE pull_request_id = $1
RETURNING pull_request_id, pull_request_name, author_id, status, assigned_reviewers, created_at, merged_at
`
	var pr PullRequest
	var createdAt time.Time
	var mergedAt sql.NullTime
	err := s.db.QueryRowContext(ctx, query, prID).
		Scan(&pr.ID, &pr.Name, &pr.AuthorID, &pr.Status, pq.Array(&pr.AssignedReviewers), &createdAt, &mergedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PullRequest{}, &Error{Code: ErrorCodeNotFound, Message: "pull request not found"}
		}
		return PullRequest{}, fmt.Errorf("merge pull request: %w", err)
	}
	pr.CreatedAt = &createdAt
	if mergedAt.Valid {
		t := mergedAt.Time
		pr.MergedAt = &t
	}
	return pr, nil
}

// ReassignReviewer reassigns a reviewer on a pull request to another active teammate.
func (s *Service) ReassignReviewer(ctx context.Context, prID, oldUserID string) (PullRequest, string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PullRequest{}, "", fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	const selectPRQuery = `
SELECT author_id, status, assigned_reviewers
FROM pull_requests
WHERE pull_request_id = $1
FOR UPDATE
`
	var authorID string
	var status string
	var assigned []string
	err = tx.QueryRowContext(ctx, selectPRQuery, prID).
		Scan(&authorID, &status, pq.Array(&assigned))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PullRequest{}, "", &Error{Code: ErrorCodeNotFound, Message: "pull request not found"}
		}
		return PullRequest{}, "", fmt.Errorf("get pull request: %w", err)
	}

	if status == "MERGED" {
		return PullRequest{}, "", &Error{Code: ErrorCodePRMerged, Message: "cannot reassign on merged PR"}
	}

	if !isReviewerAssigned(assigned, oldUserID) {
		return PullRequest{}, "", &Error{Code: ErrorCodeNotAssigned, Message: "reviewer is not assigned to this PR"}
	}

	const selectUserTeamQuery = `SELECT team_name FROM users WHERE user_id = $1`
	var teamName string
	err = tx.QueryRowContext(ctx, selectUserTeamQuery, oldUserID).Scan(&teamName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PullRequest{}, "", &Error{Code: ErrorCodeNotFound, Message: "user not found"}
		}
		return PullRequest{}, "", fmt.Errorf("get user team: %w", err)
	}

	const selectCandidateQuery = `
SELECT user_id
FROM users
WHERE team_name = $1
  AND is_active = TRUE
  AND user_id <> $2
  AND user_id <> $3
  AND NOT (user_id = ANY($4))
ORDER BY random()
LIMIT 1
`
	var newUserID string
	err = tx.QueryRowContext(ctx, selectCandidateQuery, teamName, oldUserID, authorID, pq.Array(assigned)).
		Scan(&newUserID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PullRequest{}, "", &Error{Code: ErrorCodeNoCandidate, Message: "no active replacement candidate in team"}
		}
		return PullRequest{}, "", fmt.Errorf("select replacement reviewer: %w", err)
	}

	newAssigned := replaceReviewer(assigned, oldUserID, newUserID)

	const updatePRQuery = `
UPDATE pull_requests
SET assigned_reviewers = $2
WHERE pull_request_id = $1
RETURNING pull_request_id, pull_request_name, author_id, status, assigned_reviewers, created_at, merged_at
`
	var pr PullRequest
	var createdAt time.Time
	var mergedAt sql.NullTime
	err = tx.QueryRowContext(ctx, updatePRQuery, prID, pq.Array(newAssigned)).
		Scan(&pr.ID, &pr.Name, &pr.AuthorID, &pr.Status, pq.Array(&pr.AssignedReviewers), &createdAt, &mergedAt)
	if err != nil {
		return PullRequest{}, "", fmt.Errorf("update pull request reviewers: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return PullRequest{}, "", fmt.Errorf("commit tx: %w", err)
	}

	pr.CreatedAt = &createdAt
	if mergedAt.Valid {
		t := mergedAt.Time
		pr.MergedAt = &t
	}

	return pr, newUserID, nil
}

// GetUserReviews returns pull requests where the user is assigned as a reviewer.
func (s *Service) GetUserReviews(ctx context.Context, userID string) ([]PullRequestShort, error) {
	const query = `
SELECT pull_request_id, pull_request_name, author_id, status
FROM pull_requests
WHERE $1 = ANY(assigned_reviewers)
ORDER BY pull_request_id
`
	rows, err := s.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("get user reviews: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	prs := make([]PullRequestShort, 0)

	for rows.Next() {
		var pr PullRequestShort
		if err := rows.Scan(&pr.ID, &pr.Name, &pr.AuthorID, &pr.Status); err != nil {
			return nil, fmt.Errorf("scan user reviews: %w", err)
		}
		prs = append(prs, pr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("user reviews rows: %w", err)
	}

	return prs, nil
}

// SetUserIsActive updates the is_active flag for a user and cleans up assignments if needed.
func (s *Service) SetUserIsActive(ctx context.Context, userID string, isActive bool) (User, error) {
	const query = `
UPDATE users SET is_active = $2
WHERE user_id = $1
RETURNING user_id, username, team_name, is_active
`
	var u User
	err := s.db.QueryRowContext(ctx, query, userID, isActive).
		Scan(&u.ID, &u.Name, &u.TeamName, &u.IsActive)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, &Error{Code: ErrorCodeNotFound, Message: "user not found"}
		}
		return User{}, fmt.Errorf("set is_active: %w", err)
	}

	if isActive {
		return u, nil
	}

	const updatePRsQuery = `
UPDATE pull_requests
SET assigned_reviewers = array_remove(assigned_reviewers, $1)
WHERE $1 = ANY(assigned_reviewers)
  AND status <> 'MERGED'
`
	_, err = s.db.ExecContext(ctx, updatePRsQuery, userID)
	if err != nil {
		return User{}, fmt.Errorf("remove inactive reviewer from pull requests: %w", err)
	}

	return u, nil
}

// DeactivateTeamMembers deactivates all members of a team and cleans up their assignments.
func (s *Service) DeactivateTeamMembers(ctx context.Context, teamName string) (Team, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Team{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	const selectTeamQuery = `SELECT team_name FROM teams WHERE team_name = $1`
	var existing string
	err = tx.QueryRowContext(ctx, selectTeamQuery, teamName).Scan(&existing)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Team{}, &Error{Code: ErrorCodeNotFound, Message: "team not found"}
		}
		return Team{}, fmt.Errorf("get team: %w", err)
	}

	const selectMembersQuery = `SELECT user_id, username, is_active FROM users WHERE team_name = $1 ORDER BY user_id`
	rows, err := tx.QueryContext(ctx, selectMembersQuery, teamName)
	if err != nil {
		return Team{}, fmt.Errorf("select team members: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var members []TeamMember
	var userIDs []string
	for rows.Next() {
		var m TeamMember
		if err := rows.Scan(&m.ID, &m.Name, &m.IsActive); err != nil {
			return Team{}, fmt.Errorf("scan team member: %w", err)
		}
		userIDs = append(userIDs, m.ID)
		m.IsActive = false
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		return Team{}, fmt.Errorf("members rows: %w", err)
	}

	_, err = tx.ExecContext(ctx, `UPDATE users SET is_active = FALSE WHERE team_name = $1`, teamName)
	if err != nil {
		return Team{}, fmt.Errorf("deactivate users: %w", err)
	}

	if len(userIDs) > 0 {
		const updatePRsQuery = `
UPDATE pull_requests
SET assigned_reviewers = array(
    SELECT reviewer
    FROM unnest(assigned_reviewers) AS reviewer
    WHERE NOT (reviewer = ANY($1))
)
WHERE status <> 'MERGED'
  AND assigned_reviewers && $1
`
		_, err = tx.ExecContext(ctx, updatePRsQuery, pq.Array(userIDs))
		if err != nil {
			return Team{}, fmt.Errorf("cleanup pull requests: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return Team{}, fmt.Errorf("commit tx: %w", err)
	}

	return Team{
		Name:    teamName,
		Members: members,
	}, nil
}

// UserAssignmentStat represents assignment statistics per user.
type UserAssignmentStat struct {
	UserID      string `json:"user_id"`
	Assignments int    `json:"assignments"`
}

// PRAssignmentStat represents assignment statistics per pull request.
type PRAssignmentStat struct {
	PullRequestID string `json:"pull_request_id"`
	Assignments   int    `json:"assignments"`
}

// AssignmentStats aggregates assignment statistics by user and by pull request.
type AssignmentStats struct {
	ByUser []UserAssignmentStat `json:"by_user"`
	ByPR   []PRAssignmentStat   `json:"by_pr"`
}

// GetAssignmentStats returns aggregated assignment statistics.
func (s *Service) GetAssignmentStats(ctx context.Context) (AssignmentStats, error) {
	var stats AssignmentStats

	const byUserQuery = `
SELECT reviewer_id, COUNT(*)
FROM (
  SELECT unnest(assigned_reviewers) AS reviewer_id
  FROM pull_requests
) t
GROUP BY reviewer_id
ORDER BY reviewer_id
`
	rows, err := s.db.QueryContext(ctx, byUserQuery)
	if err != nil {
		return stats, fmt.Errorf("stats by user: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	for rows.Next() {
		var st UserAssignmentStat
		if err := rows.Scan(&st.UserID, &st.Assignments); err != nil {
			return stats, fmt.Errorf("scan stats by user: %w", err)
		}
		stats.ByUser = append(stats.ByUser, st)
	}
	if err := rows.Err(); err != nil {
		return stats, fmt.Errorf("stats by user rows: %w", err)
	}

	const byPRQuery = `
SELECT pull_request_id, cardinality(assigned_reviewers) AS cnt
FROM pull_requests
ORDER BY pull_request_id
`
	rows2, err := s.db.QueryContext(ctx, byPRQuery)
	if err != nil {
		return stats, fmt.Errorf("stats by pr: %w", err)
	}
	defer func() {
		_ = rows2.Close()
	}()

	for rows2.Next() {
		var st PRAssignmentStat
		if err := rows2.Scan(&st.PullRequestID, &st.Assignments); err != nil {
			return stats, fmt.Errorf("scan stats by pr: %w", err)
		}
		stats.ByPR = append(stats.ByPR, st)
	}
	if err := rows2.Err(); err != nil {
		return stats, fmt.Errorf("stats by pr rows: %w", err)
	}

	return stats, nil
}

func isReviewerAssigned(assigned []string, oldUserID string) bool {
	for _, id := range assigned {
		if id == oldUserID {
			return true
		}
	}
	return false
}

func replaceReviewer(assigned []string, oldUserID, newUserID string) []string {
	newAssigned := make([]string, len(assigned))
	for i, id := range assigned {
		if id == oldUserID {
			newAssigned[i] = newUserID
		} else {
			newAssigned[i] = id
		}
	}
	return newAssigned
}
