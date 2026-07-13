package serve

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// A handler panic must become a 500 response, not a dropped connection: a
// reverse proxy turns the latter into a bare 502 that looks identical to the
// process being down.
func TestWithRecoveryConvertsPanicTo500(t *testing.T) {
	h := withRecovery(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestWithRecoveryPassesThroughNormalResponses(t *testing.T) {
	h := withRecovery(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want 418", rec.Code)
	}
}

// http.ErrAbortHandler is net/http's sanctioned way to abort a response; the
// middleware must re-panic so the server keeps its suppressed handling.
func TestWithRecoveryRepanicsOnErrAbortHandler(t *testing.T) {
	h := withRecovery(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	defer func() {
		if rec := recover(); rec != http.ErrAbortHandler {
			t.Fatalf("recovered %v, want http.ErrAbortHandler to propagate", rec)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	t.Fatal("expected ErrAbortHandler to propagate")
}
