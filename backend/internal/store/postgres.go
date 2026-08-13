package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql 驱动 "pgx"
)

//go:embed postgres_schema.sql
var postgresSchemaSQL string

//go:embed postgres_indexes.sql
var postgresIndexesSQL string

// PostgresStore 实现 Store，跟 SQLiteStore 平行；
// 与 SQLite 接口完全相同，main 接线层按 cfg.PostgresURL 是否非空决定走哪条。
//
// 与 SQLite 的差异（实现细节）：
//   - payload 列类型 BYTEA 而非 BLOB
//   - created_at 用 BIGINT 存纳秒（与 SQLite 同语义，便于跨库一致排序）
//   - upsert 用 `ON CONFLICT ... DO UPDATE`（PG 与 SQLite 都支持）
//   - 占位符 $1/$2/... 而非 ?
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore 用 DSN 打开 PG 连接 + 应用 schema。
// dsn 典型形态：postgres://user:pass@host:5432/dbname?sslmode=disable
// 应用 schema 用 CREATE IF NOT EXISTS，幂等可重入。
func NewPostgresStore(dsn string) (*PostgresStore, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres open: %w", err)
	}
	// 合理的默认连接池（Fly.io 单机 + 100MB PG 不需要太大）
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxIdleTime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("postgres ping: %w", err)
	}
	// Two ordered phases; the order is load-bearing because the indexes reference locale.
	// The locale backfill lives at the end of postgres_schema.sql: PG has ADD COLUMN IF NOT EXISTS.
	if _, err := db.ExecContext(ctx, postgresSchemaSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("postgres apply schema: %w", err)
	}
	if _, err := db.ExecContext(ctx, postgresIndexesSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("postgres apply indexes: %w", err)
	}
	return &PostgresStore{db: db}, nil
}

// Close 关闭底层 db handle。
func (s *PostgresStore) Close() error { return s.db.Close() }

// Ping 走 database/sql 的 PingContext；readiness handler 用。
func (s *PostgresStore) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Get 按 (owner, repo, pr_number, head_sha, locale) 查公开评审缓存；未命中返 (nil, nil)。
func (s *PostgresStore) Get(ctx context.Context, owner, repo string, pr int, headSHA, locale string) (*Record, error) {
	const q = `SELECT id, user_id, owner, repo, pr_number, head_sha, locale, payload, created_at
	           FROM reviews
	           WHERE owner = $1 AND repo = $2 AND pr_number = $3 AND head_sha = $4 AND locale = $5 AND user_id IS NULL
	           LIMIT 1`
	row := s.db.QueryRowContext(ctx, q, owner, repo, pr, headSHA, normalizeLocale(locale))
	return scanPgRecord(row)
}

// GetByID 按主键查；未命中返 (nil, nil)。
func (s *PostgresStore) GetByID(ctx context.Context, id string) (*Record, error) {
	const q = `SELECT id, user_id, owner, repo, pr_number, head_sha, locale, payload, created_at
	           FROM reviews WHERE id = $1 LIMIT 1`
	row := s.db.QueryRowContext(ctx, q, id)
	return scanPgRecord(row)
}

// Put 写入或更新；冲突时复用既有 id 仅刷新 payload + 时间，保持前端深链稳定。
// r.ID 为空时返 error（与 SQLiteStore 同语义；调用方应先 store.NewID()）。
func (s *PostgresStore) Put(ctx context.Context, r *Record) error {
	if r.ID == "" {
		return errors.New("Record.ID is empty; generate with store.NewID() first")
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	} else {
		r.CreatedAt = r.CreatedAt.UTC()
	}
	// PG 的 partial-index unique 约束，ON CONFLICT 需指定列表，按是否有 user_id 二选一
	// （与 SQLiteStore 的 WHERE 条件保持一致）
	r.Locale = normalizeLocale(r.Locale)
	const (
		qPublic = `INSERT INTO reviews (id, user_id, owner, repo, pr_number, head_sha, locale, payload, created_at)
		           VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		           ON CONFLICT (owner, repo, pr_number, head_sha, locale) WHERE user_id IS NULL
		           DO UPDATE SET payload = EXCLUDED.payload, created_at = EXCLUDED.created_at`
		qUser = `INSERT INTO reviews (id, user_id, owner, repo, pr_number, head_sha, locale, payload, created_at)
		           VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		           ON CONFLICT (user_id, owner, repo, pr_number, head_sha, locale) WHERE user_id IS NOT NULL
		           DO UPDATE SET payload = EXCLUDED.payload, created_at = EXCLUDED.created_at`
	)
	q := qPublic
	if r.UserID != nil {
		q = qUser
	}
	_, err := s.db.ExecContext(ctx, q,
		r.ID, r.UserID, r.Owner, r.Repo, r.PRNumber, r.HeadSHA, r.Locale, []byte(r.Payload), r.CreatedAt.UnixNano(),
	)
	return err
}

// List 按 user_id 过滤（nil → user_id IS NULL），按 created_at DESC 排序，最多 limit 条。
// limit ≤ 0 时强制 50（避免一次拉爆）。
func (s *PostgresStore) List(ctx context.Context, userID *string, limit int) ([]*Record, error) {
	if limit <= 0 {
		limit = 50
	}
	const (
		qAll = `SELECT id, user_id, owner, repo, pr_number, head_sha, locale, payload, created_at
		         FROM reviews WHERE user_id IS NULL
		         ORDER BY created_at DESC, id DESC LIMIT $1`
		qUser = `SELECT id, user_id, owner, repo, pr_number, head_sha, locale, payload, created_at
		         FROM reviews WHERE user_id = $1
		         ORDER BY created_at DESC, id DESC LIMIT $2`
	)
	var (
		rows *sql.Rows
		err  error
	)
	if userID == nil {
		rows, err = s.db.QueryContext(ctx, qAll, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, qUser, *userID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Record
	for rows.Next() {
		r, err := scanPgRecordRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// scanPgRecord 单行 QueryRow 扫描；未命中 (nil, nil)。
func scanPgRecord(row *sql.Row) (*Record, error) {
	var (
		r       Record
		payload []byte
		ts      int64
		userID  sql.NullString
	)
	err := row.Scan(&r.ID, &userID, &r.Owner, &r.Repo, &r.PRNumber, &r.HeadSHA, &r.Locale, &payload, &ts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if userID.Valid {
		s := userID.String
		r.UserID = &s
	}
	r.Payload = payload
	r.CreatedAt = time.Unix(0, ts).UTC()
	return &r, nil
}

// scanPgRecordRows 多行 Rows 扫描；调用方在循环里用。
func scanPgRecordRows(rows *sql.Rows) (*Record, error) {
	var (
		r       Record
		payload []byte
		ts      int64
		userID  sql.NullString
	)
	if err := rows.Scan(&r.ID, &userID, &r.Owner, &r.Repo, &r.PRNumber, &r.HeadSHA, &r.Locale, &payload, &ts); err != nil {
		return nil, err
	}
	if userID.Valid {
		s := userID.String
		r.UserID = &s
	}
	r.Payload = payload
	r.CreatedAt = time.Unix(0, ts).UTC()
	return &r, nil
}

// Delete 按 ID 硬删；不存在不算错（幂等）
func (s *PostgresStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM reviews WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("postgres delete: %w", err)
	}
	return nil
}
