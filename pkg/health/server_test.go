package health

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthEndpointReturnsOK(t *testing.T) {
	s := NewServer("127.0.0.1", 0)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	s.healthHandler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp StatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "ok" {
		t.Errorf("expected status 'ok', got %q", resp.Status)
	}
	if resp.Pid == 0 {
		t.Error("expected non-zero pid")
	}
}

func TestReadyEndpointNotReadyByDefault(t *testing.T) {
	s := NewServer("127.0.0.1", 0)

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()
	s.readyHandler(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 before ready, got %d", w.Code)
	}

	var resp StatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "not ready" {
		t.Errorf("expected 'not ready', got %q", resp.Status)
	}
}

func TestReadyEndpointAfterSetReady(t *testing.T) {
	s := NewServer("127.0.0.1", 0)
	s.SetReady(true)

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()
	s.readyHandler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 after SetReady(true), got %d", w.Code)
	}

	var resp StatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "ready" {
		t.Errorf("expected 'ready', got %q", resp.Status)
	}
}

func TestRegisterOnMuxSetsReady(t *testing.T) {
	s := NewServer("127.0.0.1", 0)

	// Before RegisterOnMux, should not be ready
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()
	s.readyHandler(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 before RegisterOnMux, got %d", w.Code)
	}

	// RegisterOnMux should mark as ready
	mux := http.NewServeMux()
	s.RegisterOnMux(mux)

	w = httptest.NewRecorder()
	s.readyHandler(w, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 after RegisterOnMux, got %d", w.Code)
	}
}

func TestReadyEndpointFailedCheck(t *testing.T) {
	s := NewServer("127.0.0.1", 0)
	s.SetReady(true)
	s.RegisterCheck("db", func() (bool, string) {
		return false, "connection refused"
	})

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()
	s.readyHandler(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 with failed check, got %d", w.Code)
	}

	var resp StatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Checks["db"].Status != "fail" {
		t.Errorf("expected check status 'fail', got %q", resp.Checks["db"].Status)
	}
	if resp.Checks["db"].Message != "connection refused" {
		t.Errorf("expected message 'connection refused', got %q", resp.Checks["db"].Message)
	}
}

func TestReadyEndpointPassingCheck(t *testing.T) {
	s := NewServer("127.0.0.1", 0)
	s.SetReady(true)
	s.RegisterCheck("db", func() (bool, string) {
		return true, "connected"
	})

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()
	s.readyHandler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with passing check, got %d", w.Code)
	}
}

func TestReloadEndpointRequiresPost(t *testing.T) {
	s := NewServer("127.0.0.1", 0)

	req := httptest.NewRequest(http.MethodGet, "/reload", nil)
	w := httptest.NewRecorder()
	s.reloadHandler(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for GET, got %d", w.Code)
	}
}

func TestReloadEndpointNoFunc(t *testing.T) {
	s := NewServer("127.0.0.1", 0)

	req := httptest.NewRequest(http.MethodPost, "/reload", nil)
	w := httptest.NewRecorder()
	s.reloadHandler(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when reload not configured, got %d", w.Code)
	}
}

func TestReloadEndpointSuccess(t *testing.T) {
	s := NewServer("127.0.0.1", 0)
	s.SetReloadFunc(func() error {
		return nil
	})

	req := httptest.NewRequest(http.MethodPost, "/reload", nil)
	w := httptest.NewRecorder()
	s.reloadHandler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on successful reload, got %d", w.Code)
	}
}

func TestSetReadyToggle(t *testing.T) {
	s := NewServer("127.0.0.1", 0)

	s.SetReady(true)
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()
	s.readyHandler(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 after SetReady(true), got %d", w.Code)
	}

	s.SetReady(false)
	w = httptest.NewRecorder()
	s.readyHandler(w, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 after SetReady(false), got %d", w.Code)
	}
}
