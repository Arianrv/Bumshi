package server

import (
	"net/http/httptest"
	"testing"

	"github.com/bumshi/bumshi/server/internal/health"
)

func TestRoutes(t *testing.T) {
	hc := health.New()
	hc.SetReady(true)
	h := routes(hc, nil, nil, nil, "/admin/")

	cases := []struct {
		path string
		want int
	}{
		{"/healthz", 200},
		{"/readyz", 200},
		{"/version", 200},
		{"/does-not-exist", 404},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", tc.path, nil))
		if rec.Code != tc.want {
			t.Errorf("GET %s = %d, want %d", tc.path, rec.Code, tc.want)
		}
	}
}

func TestReadyzReflectsCheckerState(t *testing.T) {
	hc := health.New() // not ready
	rec := httptest.NewRecorder()
	routes(hc, nil, nil, nil, "/admin/").ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != 503 {
		t.Errorf("readyz before ready = %d, want 503", rec.Code)
	}
}

func TestVersionEndpointIsJSON(t *testing.T) {
	hc := health.New()
	rec := httptest.NewRecorder()
	routes(hc, nil, nil, nil, "/admin/").ServeHTTP(rec, httptest.NewRequest("GET", "/version", nil))
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content-type = %q", ct)
	}
	if rec.Body.Len() == 0 {
		t.Error("empty version body")
	}
}
