package api

import (
	"net/http"
	"testing"

	"github.com/ecstasoy/LGTM/backend/internal/llm"
	"github.com/ecstasoy/LGTM/backend/internal/session"
)

// getReviewAs fetches a review detail, carrying the session cookie when sid is non-empty.
func getReviewAs(t *testing.T, baseURL, id, sid string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL+"/api/reviews/"+id, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if sid != "" {
		req.AddCookie(&http.Cookie{Name: session.CookieName, Value: sid})
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get review: %v", err)
	}
	res.Body.Close()
	return res.StatusCode
}

func TestGetReview_OwnedReview_VisibleOnlyToOwner(t *testing.T) {
	s := newTestStore(t)
	alice := "alice"
	id := seedSteerReviewOwnedBy(t, s, &alice)
	sm := session.New(nil, 0)
	srv := startTestServer(t, Deps{Provider: llm.NewMockProvider(), Store: s, Sessions: sm})

	cases := []struct {
		name string
		sid  string
		want int
	}{
		{"owner", loginAs(t, sm, "alice"), http.StatusOK},
		{"other user", loginAs(t, sm, "bob"), http.StatusNotFound},
		{"anonymous", "", http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := getReviewAs(t, srv.URL, id, tc.sid); got != tc.want {
				t.Errorf("status=%d, want %d", got, tc.want)
			}
		})
	}
}

func TestGetReview_AnonymousReview_VisibleWithoutLogin(t *testing.T) {
	s := newTestStore(t)
	id := seedSteerReview(t, s)
	srv := startTestServer(t, Deps{Provider: llm.NewMockProvider(), Store: s, Sessions: session.New(nil, 0)})

	if got := getReviewAs(t, srv.URL, id, ""); got != http.StatusOK {
		t.Errorf("anonymous review: status=%d, want 200", got)
	}
}
