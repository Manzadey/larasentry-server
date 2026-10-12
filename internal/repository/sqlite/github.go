package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"server/internal/domain"
)

type GitHubAccountRepo struct {
	db *sql.DB
}

func NewGitHubAccountRepo(db *sql.DB) domain.GitHubAccountRepository {
	return &GitHubAccountRepo{db: db}
}

const accountSelect = `
	SELECT a.id, a.name, a.login, a.token, a.import_all, a.synced_at, a.sync_error,
	       (SELECT COUNT(*) FROM github_repos r WHERE r.account_id = a.id),
	       (SELECT COUNT(*) FROM github_repos r WHERE r.account_id = a.id AND r.selected = 1)
	FROM github_accounts a`

const repoSelect = `
	SELECT r.id, r.account_id, a.name, a.login, r.full_name, r.default_branch, r.private, r.selected
	FROM github_repos r
	JOIN github_accounts a ON a.id = r.account_id`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAccount(s rowScanner) (domain.GitHubAccount, error) {
	var a domain.GitHubAccount
	var syncedAt sql.NullTime
	err := s.Scan(&a.ID, &a.Name, &a.Login, &a.Token, &a.ImportAll, &syncedAt, &a.SyncError, &a.ReposTotal, &a.ReposSelected)
	if syncedAt.Valid {
		a.SyncedAt = &syncedAt.Time
	}
	return a, err
}

func (r *GitHubAccountRepo) Create(ctx context.Context, name, token string) (int64, error) {
	res, err := r.db.ExecContext(ctx, "INSERT INTO github_accounts (name, token) VALUES (?, ?)", name, token)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (r *GitHubAccountRepo) Get(ctx context.Context, id int64) (*domain.GitHubAccount, error) {
	a, err := scanAccount(r.db.QueryRowContext(ctx, accountSelect+" WHERE a.id = ?", id))
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (r *GitHubAccountRepo) List(ctx context.Context) ([]domain.GitHubAccount, error) {
	rows, err := r.db.QueryContext(ctx, accountSelect+" ORDER BY a.name, a.id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accounts []domain.GitHubAccount
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, a)
	}
	return accounts, rows.Err()
}

func (r *GitHubAccountRepo) ListRepos(ctx context.Context, accountID int64) ([]domain.GitHubRepo, error) {
	return r.queryRepos(ctx, repoSelect+" WHERE r.account_id = ? ORDER BY r.full_name", accountID)
}

func (r *GitHubAccountRepo) ListSelectedRepos(ctx context.Context) ([]domain.GitHubRepo, error) {
	return r.queryRepos(ctx, repoSelect+" WHERE r.selected = 1 ORDER BY a.name, a.id, r.full_name")
}

func (r *GitHubAccountRepo) queryRepos(ctx context.Context, query string, args ...any) ([]domain.GitHubRepo, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var repos []domain.GitHubRepo
	for rows.Next() {
		var repo domain.GitHubRepo
		if err := rows.Scan(&repo.ID, &repo.AccountID, &repo.AccountName, &repo.AccountLogin,
			&repo.FullName, &repo.DefaultBranch, &repo.Private, &repo.Selected); err != nil {
			return nil, err
		}
		repos = append(repos, repo)
	}
	return repos, rows.Err()
}

func (r *GitHubAccountRepo) ApplySync(ctx context.Context, accountID int64, login string, repos []domain.GitHubRepo) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	fresh := make(map[string]bool, len(repos))
	for _, repo := range repos {
		fresh[repo.FullName] = true
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO github_repos (account_id, full_name, default_branch, private, selected)
			VALUES (?, ?, ?, ?, (SELECT import_all FROM github_accounts WHERE id = ?))
			ON CONFLICT(account_id, full_name) DO UPDATE SET
				default_branch = excluded.default_branch,
				private = excluded.private
		`, accountID, repo.FullName, repo.DefaultBranch, repo.Private, accountID); err != nil {
			return fmt.Errorf("upsert repo %s: %w", repo.FullName, err)
		}
	}

	stale, err := staleRepoIDs(ctx, tx, accountID, fresh)
	if err != nil {
		return err
	}
	for _, id := range stale {
		if err := unlinkProjects(ctx, tx, "id = ?", id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM github_repos WHERE id = ?", id); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE github_accounts SET login = ?, synced_at = ?, sync_error = '' WHERE id = ?
	`, login, time.Now(), accountID); err != nil {
		return err
	}
	return tx.Commit()
}

func staleRepoIDs(ctx context.Context, tx *sql.Tx, accountID int64, fresh map[string]bool) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, "SELECT id, full_name FROM github_repos WHERE account_id = ?", accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stale []int64
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		if !fresh[name] {
			stale = append(stale, id)
		}
	}
	return stale, rows.Err()
}

// unlinkProjects нужен, потому что PRAGMA foreign_keys выключена и каскадов в схеме нет.
func unlinkProjects(ctx context.Context, tx *sql.Tx, repoFilter string, args ...any) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE projects SET github_repo_id = NULL
		WHERE github_repo_id IN (SELECT id FROM github_repos WHERE `+repoFilter+`)
	`, args...)
	return err
}

func (r *GitHubAccountRepo) SetSyncError(ctx context.Context, accountID int64, msg string) error {
	_, err := r.db.ExecContext(ctx, "UPDATE github_accounts SET sync_error = ? WHERE id = ?", msg, accountID)
	return err
}

func (r *GitHubAccountRepo) SetSelection(ctx context.Context, accountID int64, importAll bool, selectedIDs []int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, "UPDATE github_accounts SET import_all = ? WHERE id = ?", importAll, accountID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE github_repos SET selected = ? WHERE account_id = ?", importAll, accountID); err != nil {
		return err
	}
	if !importAll {
		for _, id := range selectedIDs {
			// account_id в условии не даёт подделанной форме отметить репозиторий чужого аккаунта
			if _, err := tx.ExecContext(ctx, "UPDATE github_repos SET selected = 1 WHERE id = ? AND account_id = ?", id, accountID); err != nil {
				return err
			}
		}
	}
	if err := unlinkProjects(ctx, tx, "account_id = ? AND selected = 0", accountID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *GitHubAccountRepo) Delete(ctx context.Context, id int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := unlinkProjects(ctx, tx, "account_id = ?", id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM github_repos WHERE account_id = ?", id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM github_accounts WHERE id = ?", id); err != nil {
		return err
	}
	return tx.Commit()
}
