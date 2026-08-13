package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

// postgresTestStore 取 PG_TEST_URL；空时整个 file 的测试 skip
// CI 默认不跑（避免要求每个 PR 都启 PG service）；本地 docker compose 起 PG 后 export 即可：
//
//	export PG_TEST_URL=postgres://lgtm:lgtm@localhost:5432/lgtm?sslmode=disable
//	go test ./internal/store/ -run TestPostgres -v
func postgresTestStore(t *testing.T) *PostgresStore {
	t.Helper()
	dsn := os.Getenv("PG_TEST_URL")
	if dsn == "" {
		t.Skip("PG_TEST_URL not set; skipping postgres integration tests")
	}
	s, err := NewPostgresStore(dsn)
	if err != nil {
		t.Fatalf("NewPostgresStore: %v", err)
	}
	// 每次测试前清表，避免互相污染
	if _, err := s.db.ExecContext(context.Background(), "TRUNCATE TABLE reviews"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func pgSampleRecord(headSHA string) *Record {
	return &Record{
		ID:        NewID(),
		Owner:     "golang",
		Repo:      "go",
		PRNumber:  42,
		HeadSHA:   headSHA,
		Payload:   json.RawMessage(`{"summary":"hello"}`),
		CreatedAt: time.Unix(1700_000_000, 0).UTC(),
	}
}

func TestPostgresStore_PutGet_RoundTrip(t *testing.T) {
	s := postgresTestStore(t)
	ctx := context.Background()
	rec := pgSampleRecord("sha-roundtrip")
	if err := s.Put(ctx, rec); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := s.Get(ctx, rec.Owner, rec.Repo, rec.PRNumber, rec.HeadSHA, DefaultLocale)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatalf("get miss; want record")
	}
	if got.ID != rec.ID || string(got.Payload) != string(rec.Payload) {
		t.Errorf("roundtrip mismatch: got=%+v want=%+v", got, rec)
	}
}

func TestPostgresStore_Get_MissReturnsNilNilNotError(t *testing.T) {
	s := postgresTestStore(t)
	got, err := s.Get(context.Background(), "no", "such", 1, "sha", DefaultLocale)
	if got != nil || err != nil {
		t.Errorf("want (nil, nil), got (%v, %v)", got, err)
	}
}

func TestPostgresStore_Ping(t *testing.T) {
	s := postgresTestStore(t)
	if err := s.Ping(context.Background()); err != nil {
		t.Errorf("ping: %v", err)
	}
}

func TestPostgresStore_Put_SameSHAPreservesID(t *testing.T) {
	s := postgresTestStore(t)
	ctx := context.Background()
	rec1 := pgSampleRecord("sha-same")
	if err := s.Put(ctx, rec1); err != nil {
		t.Fatalf("put1: %v", err)
	}
	rec2 := pgSampleRecord("sha-same")
	rec2.Payload = json.RawMessage(`{"summary":"updated"}`)
	if err := s.Put(ctx, rec2); err != nil {
		t.Fatalf("put2: %v", err)
	}
	got, err := s.Get(ctx, rec1.Owner, rec1.Repo, rec1.PRNumber, rec1.HeadSHA, DefaultLocale)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatalf("get miss; want record")
	}
	if got.ID != rec1.ID {
		t.Errorf("ON CONFLICT 应保留原 ID, got=%s want=%s", got.ID, rec1.ID)
	}
	if string(got.Payload) != `{"summary":"updated"}` {
		t.Errorf("payload 未刷新: %s", got.Payload)
	}
}

func TestPostgresStore_List_OrdersByCreatedAtDesc(t *testing.T) {
	s := postgresTestStore(t)
	ctx := context.Background()
	r1 := pgSampleRecord("sha-old")
	r1.CreatedAt = time.Unix(1000, 0).UTC()
	r1.HeadSHA = "sha-old"
	r2 := pgSampleRecord("sha-new")
	r2.CreatedAt = time.Unix(2000, 0).UTC()
	r2.HeadSHA = "sha-new"
	if err := s.Put(ctx, r1); err != nil {
		t.Fatalf("put r1: %v", err)
	}
	if err := s.Put(ctx, r2); err != nil {
		t.Fatalf("put r2: %v", err)
	}
	list, err := s.List(ctx, nil, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2, got %d", len(list))
	}
	if list[0].HeadSHA != "sha-new" {
		t.Errorf("最新一条应排首位; got %s", list[0].HeadSHA)
	}
}

func TestPostgresStore_Put_EmptyIDRejected(t *testing.T) {
	s := postgresTestStore(t)
	r := pgSampleRecord("sha")
	r.ID = ""
	if err := s.Put(context.Background(), r); err == nil {
		t.Errorf("空 ID 应被拒绝")
	}
}

// 编译期断言 PostgresStore 实现 Store 接口
var _ Store = (*PostgresStore)(nil)

func TestPostgresZhAndEnReviewsCoexist(t *testing.T) {
	s := postgresTestStore(t)
	ctx := context.Background()

	for _, locale := range []string{"zh", "en"} {
		if err := s.Put(ctx, &Record{
			ID: "rec-" + locale, Owner: "o", Repo: "r", PRNumber: 1, HeadSHA: "sha",
			Locale: locale, Payload: json.RawMessage(`{"summary":"` + locale + `"}`),
			CreatedAt: time.Now(),
		}); err != nil {
			t.Fatalf("put %s: %v", locale, err)
		}
	}
	for _, locale := range []string{"zh", "en"} {
		got, err := s.Get(ctx, "o", "r", 1, "sha", locale)
		if err != nil {
			t.Fatalf("get %s: %v", locale, err)
		}
		if got == nil || got.ID != "rec-"+locale || got.Locale != locale {
			t.Fatalf("get %s returned %+v", locale, got)
		}
	}
}

// legacyPostgresSchema rebuilds the pre-i18n table: no locale column, four-column unique indexes.
var legacyPostgresSchema = []string{
	`DROP TABLE IF EXISTS reviews`,
	`CREATE TABLE reviews (
		id TEXT PRIMARY KEY, user_id TEXT, owner TEXT NOT NULL, repo TEXT NOT NULL,
		pr_number BIGINT NOT NULL, head_sha TEXT NOT NULL, payload BYTEA NOT NULL,
		created_at BIGINT NOT NULL)`,
	`CREATE UNIQUE INDEX idx_reviews_public_unique
		ON reviews(owner, repo, pr_number, head_sha) WHERE user_id IS NULL`,
	`CREATE UNIQUE INDEX idx_reviews_user_unique
		ON reviews(user_id, owner, repo, pr_number, head_sha) WHERE user_id IS NOT NULL`,
	`CREATE INDEX idx_reviews_user ON reviews(user_id, created_at DESC)`,
}

// TestPostgresSchemaApplyIsIdempotentOnLegacyDatabases mirrors the SQLite migration test:
// a pre-i18n table plus the old four-column indexes must migrate in place and survive a restart.
func TestPostgresSchemaApplyIsIdempotentOnLegacyDatabases(t *testing.T) {
	s := postgresTestStore(t)
	ctx := context.Background()

	// Tear the schema back down to its pre-i18n shape, rows included.
	for _, stmt := range legacyPostgresSchema {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("build legacy schema: %v", err)
		}
	}
	legacyCreatedAt := time.Unix(1000, 0).UTC()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO reviews (id, user_id, owner, repo, pr_number, head_sha, payload, created_at)
		 VALUES ($1, NULL, $2, $3, $4, $5, $6, $7)`,
		"rec-legacy", "o", "r", 1, "sha-legacy", []byte(`{"summary":"老记录"}`), legacyCreatedAt.UnixNano(),
	); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	dsn := os.Getenv("PG_TEST_URL")
	for i := range 2 {
		migrated, err := NewPostgresStore(dsn)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}

		legacyRec, err := migrated.Get(ctx, "o", "r", 1, "sha-legacy", "zh")
		if err != nil {
			t.Fatalf("get legacy %d: %v", i, err)
		}
		if legacyRec == nil || legacyRec.ID != "rec-legacy" || legacyRec.Locale != "zh" {
			t.Fatalf("legacy row not migrated on open %d: %+v", i, legacyRec)
		}
		if string(legacyRec.Payload) != `{"summary":"老记录"}` || !legacyRec.CreatedAt.Equal(legacyCreatedAt) {
			t.Fatalf("legacy row corrupted on open %d: %s %v", i, legacyRec.Payload, legacyRec.CreatedAt)
		}

		// Only possible once the old four-column unique index is gone.
		if err := migrated.Put(ctx, &Record{
			ID: fmt.Sprintf("rec-legacy-en-%d", i), Owner: "o", Repo: "r", PRNumber: 1,
			HeadSHA: "sha-legacy", Locale: "en", Payload: json.RawMessage(`{"summary":"en"}`),
			CreatedAt: time.Now(),
		}); err != nil {
			t.Fatalf("put en alongside legacy zh, open %d: %v", i, err)
		}
		gotEN, err := migrated.Get(ctx, "o", "r", 1, "sha-legacy", "en")
		if err != nil || gotEN == nil || gotEN.Locale != "en" {
			t.Fatalf("en review not retrievable on open %d: %+v err=%v", i, gotEN, err)
		}
		if err := migrated.Close(); err != nil {
			t.Fatalf("close %d: %v", i, err)
		}
	}
}

// pgIndexOIDs maps index name to its pg_class OID. A DROP + CREATE always mints a new OID,
// so unchanged OIDs across a startup prove that startup issued no index DDL.
func pgIndexOIDs(t *testing.T, s *PostgresStore) map[string]int64 {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT c.relname, c.oid::bigint FROM pg_class c
		 JOIN pg_index i ON i.indexrelid = c.oid
		 WHERE i.indrelid = 'reviews'::regclass`)
	if err != nil {
		t.Fatalf("read index oids: %v", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var name string
		var oid int64
		if err := rows.Scan(&name, &oid); err != nil {
			t.Fatalf("scan index oid: %v", err)
		}
		out[name] = oid
	}
	return out
}

// TestPostgresSchemaApplySkipsIndexDDLOnceMigrated is the steady-state guarantee on PG: skipping the
// index script keeps the ACCESS EXCLUSIVE lock its DROPs take off every restart after the first.
func TestPostgresSchemaApplySkipsIndexDDLOnceMigrated(t *testing.T) {
	s := postgresTestStore(t)
	ctx := context.Background()
	dsn := os.Getenv("PG_TEST_URL")

	// Tear back down to the pre-i18n shape so the next open has real work to do.
	for _, stmt := range legacyPostgresSchema {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("build legacy schema: %v", err)
		}
	}
	beforeMigration := pgIndexOIDs(t, s)

	migrated, err := NewPostgresStore(dsn)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	afterMigration := pgIndexOIDs(t, migrated)
	for _, name := range reviewUniqueIndexNames {
		if afterMigration[name] == beforeMigration[name] {
			t.Fatalf("%s was not rebuilt by the migrating startup (oid %d)", name, afterMigration[name])
		}
	}
	if err := migrated.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Second and third startups must touch nothing.
	for i := range 2 {
		restarted, err := NewPostgresStore(dsn)
		if err != nil {
			t.Fatalf("restart %d: %v", i, err)
		}
		got := pgIndexOIDs(t, restarted)
		for name, oid := range afterMigration {
			if got[name] != oid {
				t.Fatalf("restart %d rebuilt %s: oid %d -> %d", i, name, oid, got[name])
			}
		}
		if err := restarted.Close(); err != nil {
			t.Fatalf("close restart %d: %v", i, err)
		}
	}
}
