package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wordbreak/backend/internal/dictionary"
	"github.com/wordbreak/backend/internal/grid"
	"github.com/wordbreak/backend/internal/store"
)

func testServer(t *testing.T, adminToken string) *Server {
	t.Helper()
	dict := dictionary.NewFromWords([]string{"CAT", "DOG"})
	return New(dict, grid.NewTrie(dict.AllWords()), store.New(), nil, nil, Config{AdminToken: adminToken})
}

func doCreatePool(t *testing.T, s *Server, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/admin/pool/create", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Admin-Token", token)
	}
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	return rec
}

func TestListPools_RequiresAdminToken(t *testing.T) {
	s := testServer(t, "secret")
	req := httptest.NewRequest(http.MethodGet, "/api/admin/pool/list", nil)
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with no token, got %d: %s", rec.Code, rec.Body)
	}
}

func TestListPools_EmptyByDefault(t *testing.T) {
	s := testServer(t, "secret")
	req := httptest.NewRequest(http.MethodGet, "/api/admin/pool/list", nil)
	req.Header.Set("X-Admin-Token", "secret")
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"pools":[]`) && !strings.Contains(rec.Body.String(), `"pools":null`) {
		t.Fatalf("expected an empty pools list for a fresh store, got %s", rec.Body)
	}
}

func TestCreatePool_DisabledWithoutServerAdminToken(t *testing.T) {
	s := testServer(t, "") // AdminToken never configured on the server at all
	rec := doCreatePool(t, s, "anything", `{"entryFee":"10000000000000000","days":1}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when the server has no admin token configured, got %d: %s", rec.Code, rec.Body)
	}
}

func TestCreatePool_RequiresAdminToken(t *testing.T) {
	s := testServer(t, "secret")
	rec := doCreatePool(t, s, "", `{"entryFee":"10000000000000000","days":1}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with no token on the request, got %d: %s", rec.Code, rec.Body)
	}
}

func TestCreatePool_RejectsWrongAdminToken(t *testing.T) {
	s := testServer(t, "secret")
	rec := doCreatePool(t, s, "wrong", `{"entryFee":"10000000000000000","days":1}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong token, got %d: %s", rec.Code, rec.Body)
	}
}

func TestCreatePool_RequiresWriterConfigured(t *testing.T) {
	s := testServer(t, "secret") // no EnableRoomStaking call -> s.writer is nil
	rec := doCreatePool(t, s, "secret", `{"entryFee":"10000000000000000","days":1}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when the operator writer isn't configured, got %d: %s", rec.Code, rec.Body)
	}
}

// Below: validation errors happen before the writer-nil check would otherwise short-circuit
// them, so they surface even without EnableRoomStaking -- confirms the contract's own
// InvalidEntryFee revert never has to be the way a caller finds out entryFee=0 is wrong.
func TestCreatePool_ValidatesEntryFee(t *testing.T) {
	s := testServer(t, "secret")
	for _, body := range []string{
		`{"entryFee":"0","days":1}`,
		`{"entryFee":"-5","days":1}`,
		`{"entryFee":"not-a-number","days":1}`,
	} {
		rec := doCreatePool(t, s, "secret", body)
		if rec.Code == http.StatusServiceUnavailable {
			t.Fatalf("body %q: expected entryFee validation (400) to run before the writer check (503), got 503", body)
		}
	}
}
