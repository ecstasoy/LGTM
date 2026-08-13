package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func sampleRecord(headSHA string) *Record {
	return &Record{
		ID:       NewID(),
		Owner:    "golang",
		Repo:     "go",
		PRNumber: 42,
		HeadSHA:  headSHA,
		Payload:  json.RawMessage(`{"summary":"ok","risks":[],"suggestions":[]}`),
	}
}

func TestSQLiteStore_PutGet_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	in := sampleRecord("sha-A")
	if err := s.Put(ctx, in); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := s.Get(ctx, "golang", "go", 42, "sha-A", DefaultLocale)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get 未命中刚写入的记录")
	}
	if got.ID != in.ID {
		t.Errorf("ID 不一致: got=%s, want=%s", got.ID, in.ID)
	}
	if string(got.Payload) != string(in.Payload) {
		t.Errorf("Payload 不一致: got=%s", got.Payload)
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt 应被自动填充")
	}
}

func TestSQLiteStore_Get_MissReturnsNilNilNotError(t *testing.T) {
	s := newTestStore(t)

	got, err := s.Get(context.Background(), "x", "y", 1, "nope", DefaultLocale)
	if err != nil {
		t.Fatalf("Get 未命中不应报错，得到 %v", err)
	}
	if got != nil {
		t.Errorf("Get 未命中应返 nil，得到 %+v", got)
	}
}

func TestSQLiteStore_Put_SameSHAPreservesID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	in1 := sampleRecord("sha-X")
	if err := s.Put(ctx, in1); err != nil {
		t.Fatalf("Put 1: %v", err)
	}

	// 同 (owner, repo, pr, sha) 二次写入：ID 复用，payload + created_at 刷新
	in2 := &Record{
		ID:    NewID(), // 新生成的 ID 应被 ON CONFLICT 忽略
		Owner: in1.Owner, Repo: in1.Repo, PRNumber: in1.PRNumber, HeadSHA: in1.HeadSHA,
		Payload: json.RawMessage(`{"summary":"updated"}`),
	}
	if err := s.Put(ctx, in2); err != nil {
		t.Fatalf("Put 2: %v", err)
	}

	got, err := s.Get(ctx, in1.Owner, in1.Repo, in1.PRNumber, in1.HeadSHA, DefaultLocale)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != in1.ID {
		t.Errorf("ID 应保持首次写入的 %s，得到 %s", in1.ID, got.ID)
	}
	if string(got.Payload) != string(in2.Payload) {
		t.Errorf("Payload 应被刷新为 in2，得到 %s", got.Payload)
	}
}

func TestSQLiteStore_Put_EmptyIDRejected(t *testing.T) {
	s := newTestStore(t)
	r := sampleRecord("sha")
	r.ID = ""
	if err := s.Put(context.Background(), r); err == nil {
		t.Error("空 ID 应被拒绝")
	}
}

func TestSQLiteStore_List_OrdersByCreatedAtDesc(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 用显式 CreatedAt 避免 ULID 时间精度相同导致的不稳定
	older := sampleRecord("sha-old")
	older.CreatedAt = time.Unix(1000, 0)
	if err := s.Put(ctx, older); err != nil {
		t.Fatalf("Put older: %v", err)
	}

	newer := sampleRecord("sha-new")
	newer.PRNumber = 43 // 避开 UNIQUE
	newer.CreatedAt = time.Unix(2000, 0)
	if err := s.Put(ctx, newer); err != nil {
		t.Fatalf("Put newer: %v", err)
	}

	got, err := s.List(ctx, nil, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List 应返 2 条，得到 %d", len(got))
	}
	if got[0].HeadSHA != "sha-new" {
		t.Errorf("最新一条应排首位，得到 %s", got[0].HeadSHA)
	}
}

func TestSQLiteStore_List_FiltersByUserID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	alice := "alice"
	pubRec := sampleRecord("sha-shared")
	if err := s.Put(ctx, pubRec); err != nil {
		t.Fatalf("Put pub: %v", err)
	}
	userRec := sampleRecord("sha-shared")
	userRec.UserID = &alice
	if err := s.Put(ctx, userRec); err != nil {
		t.Fatalf("Put user: %v", err)
	}

	pubList, _ := s.List(ctx, nil, 10)
	if len(pubList) != 1 || pubList[0].ID != pubRec.ID {
		t.Errorf("user_id=nil 应只返公共记录，得到 %+v", pubList)
	}
	aliceList, _ := s.List(ctx, &alice, 10)
	if len(aliceList) != 1 || aliceList[0].ID != userRec.ID {
		t.Errorf("user_id=alice 应只返 alice 的记录，得到 %+v", aliceList)
	}

	got, err := s.Get(ctx, pubRec.Owner, pubRec.Repo, pubRec.PRNumber, pubRec.HeadSHA, DefaultLocale)
	if err != nil {
		t.Fatalf("Get public: %v", err)
	}
	if got == nil || got.ID != pubRec.ID {
		t.Errorf("Get 应命中公共记录，得到 %+v", got)
	}
}

func TestSQLiteStore_List_SameTimestampUsesStableTieBreaker(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	ts := time.Unix(2000, 0)
	first := sampleRecord("sha-first")
	first.CreatedAt = ts
	if err := s.Put(ctx, first); err != nil {
		t.Fatalf("Put first: %v", err)
	}

	second := sampleRecord("sha-second")
	second.PRNumber = 43
	second.CreatedAt = ts
	if err := s.Put(ctx, second); err != nil {
		t.Fatalf("Put second: %v", err)
	}

	got, err := s.List(ctx, nil, 10)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List 应返 2 条，得到 %d", len(got))
	}
	if got[0].HeadSHA != "sha-second" {
		t.Errorf("同时间戳时后写入记录应排首位，得到 %s", got[0].HeadSHA)
	}
}

func TestSQLiteStore_List_LimitDefaultsTo50(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for i := range 60 {
		r := sampleRecord("sha")
		r.PRNumber = i + 1
		if err := s.Put(ctx, r); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}
	got, _ := s.List(ctx, nil, 0)
	if len(got) != 50 {
		t.Errorf("limit=0 应回落 50，得到 %d", len(got))
	}
}

func TestSQLiteStore_GetByID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	in := sampleRecord("sha")
	if err := s.Put(ctx, in); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, err := s.GetByID(ctx, in.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got == nil || got.ID != in.ID {
		t.Errorf("GetByID 命中错: %+v", got)
	}

	miss, err := s.GetByID(ctx, "no-such-id")
	if err != nil {
		t.Fatalf("GetByID miss 不应报错: %v", err)
	}
	if miss != nil {
		t.Errorf("GetByID miss 应返 nil")
	}
}

func TestNewID_UniqueAndSortable(t *testing.T) {
	a := NewID()
	time.Sleep(2 * time.Millisecond)
	b := NewID()

	if a == b {
		t.Error("ULID 不该重复")
	}
	if len(a) != 26 || len(b) != 26 {
		t.Errorf("ULID 长度应为 26, got %d / %d", len(a), len(b))
	}
	if a >= b {
		t.Errorf("先生成的 ULID 应字典序在前: %s >= %s", a, b)
	}
}

func TestZhAndEnReviewsCoexist(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	zh := &Record{
		ID: "rec-zh", Owner: "o", Repo: "r", PRNumber: 1, HeadSHA: "sha",
		Locale: "zh", Payload: json.RawMessage(`{"summary":"zh"}`), CreatedAt: time.Now(),
	}
	en := &Record{
		ID: "rec-en", Owner: "o", Repo: "r", PRNumber: 1, HeadSHA: "sha",
		Locale: "en", Payload: json.RawMessage(`{"summary":"en"}`), CreatedAt: time.Now(),
	}
	if err := s.Put(ctx, zh); err != nil {
		t.Fatalf("put zh: %v", err)
	}
	if err := s.Put(ctx, en); err != nil {
		t.Fatalf("put en: %v", err)
	}

	got, err := s.Get(ctx, "o", "r", 1, "sha", "en")
	if err != nil {
		t.Fatalf("get en: %v", err)
	}
	if got == nil || got.ID != "rec-en" {
		t.Fatalf("get en returned %+v, want rec-en", got)
	}
	if got.Locale != "en" || string(got.Payload) != `{"summary":"en"}` {
		t.Fatalf("get en returned locale=%q payload=%s", got.Locale, got.Payload)
	}

	got, err = s.Get(ctx, "o", "r", 1, "sha", "zh")
	if err != nil {
		t.Fatalf("get zh: %v", err)
	}
	if got == nil || got.ID != "rec-zh" {
		t.Fatalf("get zh returned %+v, want rec-zh", got)
	}
	if got.Locale != "zh" || string(got.Payload) != `{"summary":"zh"}` {
		t.Fatalf("get zh returned locale=%q payload=%s", got.Locale, got.Payload)
	}
}

// TestZhAndEnUserReviewsCoexist covers the second unique index, the user_id IS NOT NULL one.
func TestZhAndEnUserReviewsCoexist(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	alice := "alice"

	for _, locale := range []string{"zh", "en"} {
		if err := s.Put(ctx, &Record{
			ID: "rec-" + locale, UserID: &alice, Owner: "o", Repo: "r", PRNumber: 1,
			HeadSHA: "sha", Locale: locale, Payload: json.RawMessage(`{}`), CreatedAt: time.Now(),
		}); err != nil {
			t.Fatalf("put %s: %v", locale, err)
		}
	}
	list, err := s.List(ctx, &alice, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want both locales stored for alice, got %d rows", len(list))
	}
	seen := map[string]bool{}
	for _, r := range list {
		seen[r.Locale] = true
	}
	if !seen["zh"] || !seen["en"] {
		t.Fatalf("List did not return both locales: %+v", seen)
	}
}

// legacySchemaSQL is the pre-i18n reviews table: no locale column, four-column unique indexes.
const legacySchemaSQL = `
CREATE TABLE reviews (
	id TEXT PRIMARY KEY, user_id TEXT, owner TEXT NOT NULL, repo TEXT NOT NULL,
	pr_number INTEGER NOT NULL, head_sha TEXT NOT NULL, payload BLOB NOT NULL,
	created_at INTEGER NOT NULL);
CREATE UNIQUE INDEX idx_reviews_public_unique
	ON reviews(owner, repo, pr_number, head_sha) WHERE user_id IS NULL;
CREATE UNIQUE INDEX idx_reviews_user_unique
	ON reviews(user_id, owner, repo, pr_number, head_sha) WHERE user_id IS NOT NULL;
CREATE INDEX idx_reviews_user ON reviews(user_id, created_at DESC);
`

func TestSchemaApplyIsIdempotentOnLegacyDatabases(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.db")

	// Build a pre-i18n database: reviews without a locale column, plus one existing Chinese review.
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec(legacySchemaSQL); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	legacyCreatedAt := time.Unix(1000, 0).UTC()
	if _, err := db.Exec(
		`INSERT INTO reviews (id, user_id, owner, repo, pr_number, head_sha, payload, created_at)
		 VALUES (?, NULL, ?, ?, ?, ?, ?, ?)`,
		"rec-legacy", "o", "r", 1, "sha-legacy", []byte(`{"summary":"老记录"}`), legacyCreatedAt.UnixNano(),
	); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Opening twice must both migrate cleanly and stay idempotent.
	for i := range 2 {
		s, err := NewSQLiteStore(path)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		ctx := context.Background()

		if err := s.Put(ctx, &Record{
			ID: fmt.Sprintf("rec-%d", i), Owner: "o", Repo: "r", PRNumber: 1,
			HeadSHA: fmt.Sprintf("sha-%d", i), Locale: "en",
			Payload: json.RawMessage(`{}`), CreatedAt: time.Now(),
		}); err != nil {
			t.Fatalf("put %d: %v", i, err)
		}

		// The pre-i18n row survived the migration and is now labelled zh.
		legacy, err := s.Get(ctx, "o", "r", 1, "sha-legacy", "zh")
		if err != nil {
			t.Fatalf("get legacy %d: %v", i, err)
		}
		if legacy == nil || legacy.ID != "rec-legacy" {
			t.Fatalf("legacy row lost on open %d: %+v", i, legacy)
		}
		if legacy.Locale != "zh" {
			t.Fatalf("legacy row locale = %q on open %d, want zh", legacy.Locale, i)
		}
		if string(legacy.Payload) != `{"summary":"老记录"}` || !legacy.CreatedAt.Equal(legacyCreatedAt) {
			t.Fatalf("legacy row corrupted on open %d: payload=%s created_at=%v", i, legacy.Payload, legacy.CreatedAt)
		}

		// An en review of the same PR as the legacy zh one: only possible once the old
		// four-column unique index is gone.
		if err := s.Put(ctx, &Record{
			ID: fmt.Sprintf("rec-legacy-en-%d", i), Owner: "o", Repo: "r", PRNumber: 1,
			HeadSHA: "sha-legacy", Locale: "en", Payload: json.RawMessage(`{"summary":"en"}`),
			CreatedAt: time.Now(),
		}); err != nil {
			t.Fatalf("put en alongside legacy zh, open %d: %v", i, err)
		}
		gotEN, err := s.Get(ctx, "o", "r", 1, "sha-legacy", "en")
		if err != nil {
			t.Fatalf("get en %d: %v", i, err)
		}
		if gotEN == nil || gotEN.Locale != "en" {
			t.Fatalf("en review not retrievable on open %d: %+v", i, gotEN)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("close %d: %v", i, err)
		}
	}
}

// TestPutDefaultsEmptyLocaleToZh pins the compatibility rule for callers that predate i18n.
func TestPutDefaultsEmptyLocaleToZh(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	in := sampleRecord("sha-default")
	if err := s.Put(ctx, in); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := s.Get(ctx, in.Owner, in.Repo, in.PRNumber, in.HeadSHA, "")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil || got.Locale != "zh" {
		t.Fatalf("empty locale should be stored and read back as zh, got %+v", got)
	}
	if byID, err := s.GetByID(ctx, in.ID); err != nil || byID == nil || byID.Locale != "zh" {
		t.Fatalf("GetByID must select locale, got %+v err=%v", byID, err)
	}
	list, err := s.List(ctx, nil, 10)
	if err != nil || len(list) != 1 || list[0].Locale != "zh" {
		t.Fatalf("List must select locale, got %+v err=%v", list, err)
	}
}
