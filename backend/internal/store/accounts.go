package store

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/offlinebot/home-projects/backend/internal/model"
)

// This file holds the storage half of the single-use credential rule
// (PROMPT section 5). The rule is enforced in SQL, not in memory:
//
//	ReserveAttempt   marks the account before the credentials are sent, and
//	                 fails if another run already holds the mark.
//	ConfirmSuccess   clears the mark — only an unambiguous success does that.
//	ConsumeSecret    deletes the secret and pauses the schedulers, for every
//	                 other outcome, including a crash noticed at the next start.

var (
	// ErrNoSecret means there is nothing to try with. It is deliberately not
	// retryable: someone has to type the password in again.
	ErrNoSecret = errors.New("this account has no stored password — enter it again")
	// ErrAttemptInFlight means another run holds the account.
	ErrAttemptInFlight = errors.New("another attempt on this account is already running")
)

const accountCols = `id, kind, title, config, state, (secret_enc IS NOT NULL) AS has_secret,
	needs_secret, attempt_in_flight, consumed_at, last_ok_at, last_error, created_at, updated_at`

func scanAccount(r scanner) (*model.Account, error) {
	var a model.Account
	err := r.Scan(&a.ID, &a.Kind, &a.Title, &a.Config, &a.State, &a.HasSecret, &a.NeedsSecret,
		&a.AttemptInFlight, &a.ConsumedAt, &a.LastOKAt, &a.LastError, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, norm(err)
	}
	return &a, nil
}

func (s *Store) CreateAccount(ctx context.Context, ownerID uuid.UUID, kind, title string, config json.RawMessage, secret []byte) (*model.Account, error) {
	if len(config) == 0 {
		config = json.RawMessage(`{}`)
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO accounts (owner_id, kind, title, config, secret_enc, needs_secret, state)
		VALUES ($1,$2,$3,$4,$5,$6,'new') RETURNING `+accountCols,
		ownerID, kind, title, config, secret, len(secret) == 0)
	return scanAccount(row)
}

func (s *Store) AccountByID(ctx context.Context, id uuid.UUID) (*model.Account, error) {
	return scanAccount(s.pool.QueryRow(ctx, `SELECT `+accountCols+` FROM accounts WHERE id=$1`, id))
}

func (s *Store) ListAccounts(ctx context.Context) ([]model.Account, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+accountCols+` FROM accounts ORDER BY lower(title)`)
	if err != nil {
		return nil, norm(err)
	}
	defer rows.Close()
	out := []model.Account{}
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	if err := norm(rows.Err()); err != nil {
		return nil, err
	}
	counts, err := s.schedulerCountsByAccount(ctx)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].SchedulerCount = counts[out[i].ID]
	}
	return out, nil
}

func (s *Store) UpdateAccountConfig(ctx context.Context, id uuid.UUID, title string, config json.RawMessage) (*model.Account, error) {
	row := s.pool.QueryRow(ctx, `
		UPDATE accounts SET title=$2, config=$3, updated_at=now() WHERE id=$1
		RETURNING `+accountCols, id, title, config)
	return scanAccount(row)
}

// SetAccountSecret is the only way back after a consumed credential: the
// password is typed in again.
func (s *Store) SetAccountSecret(ctx context.Context, id uuid.UUID, secret []byte) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE accounts
		SET secret_enc=$2, needs_secret=false, state='new', last_error='',
		    attempt_in_flight=false, attempt_started_at=NULL, consumed_at=NULL, updated_at=now()
		WHERE id=$1`, id, secret)
	if err != nil {
		return norm(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	// Schedulers that were paused because the credential went missing may run
	// again — but only those, and only with a fresh password.
	_, err = s.pool.Exec(ctx, `
		UPDATE schedulers SET paused_reason='', enabled=true, updated_at=now()
		WHERE account_id=$1 AND paused_reason LIKE 'credential%'`, id)
	return norm(err)
}

func (s *Store) DeleteAccount(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM accounts WHERE id=$1`, id)
	if err != nil {
		return norm(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ReserveAttempt marks the account as "attempt in flight" and hands back the
// encrypted secret. It is a single atomic statement, so two schedulers can
// never both get one.
func (s *Store) ReserveAttempt(ctx context.Context, id uuid.UUID) ([]byte, error) {
	var secret []byte
	err := s.pool.QueryRow(ctx, `
		UPDATE accounts
		SET attempt_in_flight=true, attempt_started_at=now(), updated_at=now()
		WHERE id=$1 AND attempt_in_flight=false AND secret_enc IS NOT NULL AND needs_secret=false
		RETURNING secret_enc`, id).Scan(&secret)
	if err != nil {
		if errors.Is(norm(err), ErrNotFound) {
			// Distinguish the two reasons so the message can say which.
			a, aerr := s.AccountByID(ctx, id)
			if aerr != nil {
				return nil, aerr
			}
			if a.AttemptInFlight {
				return nil, ErrAttemptInFlight
			}
			return nil, ErrNoSecret
		}
		return nil, norm(err)
	}
	return secret, nil
}

// ConfirmSuccess is called only for an unambiguous "signed in".
func (s *Store) ConfirmSuccess(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE accounts
		SET attempt_in_flight=false, attempt_started_at=NULL, state='ok',
		    last_ok_at=now(), last_error='', updated_at=now()
		WHERE id=$1`, id)
	return norm(err)
}

// ConsumeSecret deletes the stored password. Every outcome that is not a
// confirmed success ends here — wrong password, timeout, abort, an ambiguous
// answer, or a crash discovered at the next start.
func (s *Store) ConsumeSecret(ctx context.Context, id uuid.UUID, reason string) error {
	if reason == "" {
		reason = "the attempt did not end in a confirmed sign-in"
	}
	return s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			UPDATE accounts
			SET secret_enc=NULL, needs_secret=true, state='needs_password',
			    attempt_in_flight=false, attempt_started_at=NULL, consumed_at=now(),
			    last_error=$2, updated_at=now()
			WHERE id=$1`, id, reason); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			UPDATE schedulers
			SET enabled=false, paused_reason='credential was used up: ' || $2, updated_at=now()
			WHERE account_id=$1`, id, reason)
		return err
	})
}

// RecoverInFlight is run once at startup. A mark that survived a restart means
// the attempt never came back with a success, so the secret counts as used.
func (s *Store) RecoverInFlight(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM accounts WHERE attempt_in_flight=true`)
	if err != nil {
		return nil, norm(err)
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, norm(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := norm(rows.Err()); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if err := s.ConsumeSecret(ctx, id,
			"the server stopped while an attempt was running, so the password counts as used"); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

// ---------------------------------------------------------------- schedulers

const schedulerCols = `s.id, s.project_id, s.account_id, s.title, s.kind, s.schedule, s.target_path,
	s.options, s.enabled, s.paused_reason, s.last_run_at, s.last_status, s.created_at, s.updated_at`

const schedulerSelect = `SELECT ` + schedulerCols + `, COALESCE(p.slug,''), COALESCE(a.title,'')
	FROM schedulers s
	LEFT JOIN projects p ON p.id = s.project_id
	LEFT JOIN accounts a ON a.id = s.account_id`

func scanScheduler(r scanner) (*model.Scheduler, error) {
	var s model.Scheduler
	err := r.Scan(&s.ID, &s.ProjectID, &s.AccountID, &s.Title, &s.Kind, &s.Schedule, &s.TargetPath,
		&s.Options, &s.Enabled, &s.PausedFor, &s.LastRunAt, &s.LastStatus, &s.CreatedAt, &s.UpdatedAt,
		&s.ProjectSlug, &s.AccountName)
	if err != nil {
		return nil, norm(err)
	}
	return &s, nil
}

type NewScheduler struct {
	OwnerID    uuid.UUID
	ProjectID  uuid.UUID
	AccountID  *uuid.UUID
	Title      string
	Kind       string
	Schedule   string
	TargetPath string
	Options    json.RawMessage
	Enabled    bool
}

func (s *Store) CreateScheduler(ctx context.Context, in NewScheduler) (*model.Scheduler, error) {
	if len(in.Options) == 0 {
		in.Options = json.RawMessage(`{}`)
	}
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `
		INSERT INTO schedulers (owner_id, project_id, account_id, title, kind, schedule, target_path, options, enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
		in.OwnerID, in.ProjectID, in.AccountID, in.Title, in.Kind, in.Schedule, in.TargetPath,
		in.Options, in.Enabled).Scan(&id)
	if err != nil {
		return nil, norm(err)
	}
	return s.SchedulerByID(ctx, id)
}

func (s *Store) SchedulerByID(ctx context.Context, id uuid.UUID) (*model.Scheduler, error) {
	return scanScheduler(s.pool.QueryRow(ctx, schedulerSelect+` WHERE s.id=$1`, id))
}

func (s *Store) querySchedulers(ctx context.Context, q string, args ...any) ([]model.Scheduler, error) {
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, norm(err)
	}
	defer rows.Close()
	out := []model.Scheduler{}
	for rows.Next() {
		sc, err := scanScheduler(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sc)
	}
	return out, norm(rows.Err())
}

func (s *Store) ListSchedulers(ctx context.Context) ([]model.Scheduler, error) {
	return s.querySchedulers(ctx, schedulerSelect+` ORDER BY s.created_at`)
}

func (s *Store) ListSchedulersForProject(ctx context.Context, projectID uuid.UUID) ([]model.Scheduler, error) {
	return s.querySchedulers(ctx, schedulerSelect+` WHERE s.project_id=$1 ORDER BY s.created_at`, projectID)
}

func (s *Store) ListSchedulersForAccount(ctx context.Context, accountID uuid.UUID) ([]model.Scheduler, error) {
	return s.querySchedulers(ctx, schedulerSelect+` WHERE s.account_id=$1 ORDER BY s.created_at`, accountID)
}

func (s *Store) schedulerCountsByAccount(ctx context.Context) (map[uuid.UUID]int, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT account_id, count(*) FROM schedulers WHERE account_id IS NOT NULL GROUP BY account_id`)
	if err != nil {
		return nil, norm(err)
	}
	defer rows.Close()
	out := map[uuid.UUID]int{}
	for rows.Next() {
		var id uuid.UUID
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, norm(err)
		}
		out[id] = n
	}
	return out, norm(rows.Err())
}

type SchedulerPatch struct {
	AccountID   **uuid.UUID
	Title       *string
	Schedule    *string
	TargetPath  *string
	Options     *json.RawMessage
	Enabled     *bool
	PausedFor   *string
	LastStatus  *string
	MarkRunNow  bool
	ProjectRead bool
}

func (s *Store) UpdateScheduler(ctx context.Context, id uuid.UUID, p SchedulerPatch) (*model.Scheduler, error) {
	set := ""
	args := []any{id}
	add := func(col string, val any) {
		args = append(args, val)
		if set != "" {
			set += ", "
		}
		set += col + " = $" + strconv.Itoa(len(args))
	}
	if p.AccountID != nil {
		add("account_id", *p.AccountID)
	}
	if p.Title != nil {
		add("title", *p.Title)
	}
	if p.Schedule != nil {
		add("schedule", *p.Schedule)
	}
	if p.TargetPath != nil {
		add("target_path", *p.TargetPath)
	}
	if p.Options != nil {
		add("options", *p.Options)
	}
	if p.Enabled != nil {
		add("enabled", *p.Enabled)
	}
	if p.PausedFor != nil {
		add("paused_reason", *p.PausedFor)
	}
	if p.LastStatus != nil {
		add("last_status", *p.LastStatus)
	}
	if p.MarkRunNow {
		if set != "" {
			set += ", "
		}
		set += "last_run_at = now()"
	}
	if set == "" {
		return s.SchedulerByID(ctx, id)
	}
	if _, err := s.pool.Exec(ctx,
		`UPDATE schedulers SET `+set+`, updated_at=now() WHERE id=$1`, args...); err != nil {
		return nil, norm(err)
	}
	return s.SchedulerByID(ctx, id)
}

func (s *Store) DeleteScheduler(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM schedulers WHERE id=$1`, id)
	if err != nil {
		return norm(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// PauseSchedulersForProject is used when a project is frozen: schedulers that
// would write into it are paused and named, not silently skipped.
func (s *Store) PauseSchedulersForProject(ctx context.Context, projectID uuid.UUID, reason string) ([]model.Scheduler, error) {
	list, err := s.ListSchedulersForProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return list, nil
	}
	_, err = s.pool.Exec(ctx,
		`UPDATE schedulers SET enabled=false, paused_reason=$2, updated_at=now() WHERE project_id=$1`,
		projectID, reason)
	return list, norm(err)
}

// ------------------------------------------------------------ scheduler runs

func (s *Store) StartRun(ctx context.Context, schedulerID uuid.UUID, trigger string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx,
		`INSERT INTO scheduler_runs (scheduler_id, trigger) VALUES ($1,$2) RETURNING id`,
		schedulerID, trigger).Scan(&id)
	return id, norm(err)
}

func (s *Store) FinishRun(ctx context.Context, runID int64, status, message string, filesChanged int, log string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE scheduler_runs
		SET finished_at=now(), status=$2, message=$3, files_changed=$4, log=$5
		WHERE id=$1`, runID, status, message, filesChanged, log)
	return norm(err)
}

func (s *Store) ListRuns(ctx context.Context, schedulerID uuid.UUID, limit int) ([]model.SchedulerRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, scheduler_id, started_at, finished_at, status, message, files_changed, trigger, log
		FROM scheduler_runs WHERE scheduler_id=$1 ORDER BY started_at DESC LIMIT $2`,
		schedulerID, limit)
	if err != nil {
		return nil, norm(err)
	}
	defer rows.Close()
	out := []model.SchedulerRun{}
	for rows.Next() {
		var r model.SchedulerRun
		if err := rows.Scan(&r.ID, &r.SchedulerID, &r.StartedAt, &r.FinishedAt, &r.Status,
			&r.Message, &r.FilesChanged, &r.Trigger, &r.Log); err != nil {
			return nil, norm(err)
		}
		out = append(out, r)
	}
	return out, norm(rows.Err())
}

// RecentRuns feeds the "failed runs are visible" panel.
func (s *Store) RecentRuns(ctx context.Context, limit int) ([]model.SchedulerRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, scheduler_id, started_at, finished_at, status, message, files_changed, trigger, log
		FROM scheduler_runs ORDER BY started_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, norm(err)
	}
	defer rows.Close()
	out := []model.SchedulerRun{}
	for rows.Next() {
		var r model.SchedulerRun
		if err := rows.Scan(&r.ID, &r.SchedulerID, &r.StartedAt, &r.FinishedAt, &r.Status,
			&r.Message, &r.FilesChanged, &r.Trigger, &r.Log); err != nil {
			return nil, norm(err)
		}
		out = append(out, r)
	}
	return out, norm(rows.Err())
}
