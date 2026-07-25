package health

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLiveAlwaysOK(t *testing.T) {
	c := New()
	rec := httptest.NewRecorder()
	c.Live(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("Live status = %d, want 200", rec.Code)
	}
}

func TestReadyReflectsState(t *testing.T) {
	c := New()

	rec := httptest.NewRecorder()
	c.Ready(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("initial Ready status = %d, want 503", rec.Code)
	}

	c.SetReady(true)
	rec = httptest.NewRecorder()
	c.Ready(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("Ready status after SetReady(true) = %d, want 200", rec.Code)
	}

	c.SetReady(false)
	rec = httptest.NewRecorder()
	c.Ready(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("Ready status after SetReady(false) = %d, want 503", rec.Code)
	}
}
