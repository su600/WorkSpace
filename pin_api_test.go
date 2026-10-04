package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func setupPinTest(t *testing.T) func() {
	t.Helper()
	previousDir := cfgDirectory
	previousPaths := pinnedPaths
	previousVersion := pinCacheVer
	previousCachedVersion := pinCachedVer
	previousHTML := pinCachedHTML
	tmp := t.TempDir()
	cfgDirectory = tmp
	pinnedPaths = nil
	pinCacheVer = 0
	pinCachedVer = 0
	pinCachedHTML = ""
	t.Cleanup(func() {
		cfgDirectory = previousDir
		pinnedPaths = previousPaths
		pinCacheVer = previousVersion
		pinCachedVer = previousCachedVersion
		pinCachedHTML = previousHTML
	})
	return func() {}
}

func pinRequest(t *testing.T, body map[string]string, authenticated bool) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/pin", bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if authenticated {
		session, _ := createSession(false)
		req.AddCookie(&http.Cookie{Name: "workspace_session", Value: session})
	}
	w := httptest.NewRecorder()
	pinHandler(w, req)
	return w
}

func TestPinHandlerJSONPinAndUnpin(t *testing.T) {
	setupPinTest(t)
	if err := os.WriteFile(filepath.Join(cfgDirectory, "note.md"), []byte("note"), 0600); err != nil {
		t.Fatal(err)
	}
	pin := pinRequest(t, map[string]string{"action": "pin", "path": "note.md"}, true)
	if pin.Code != http.StatusOK || !isPinned("note.md") {
		t.Fatalf("pin: status=%d pinned=%t body=%s", pin.Code, isPinned("note.md"), pin.Body.String())
	}
	var pinResult map[string]any
	if err := json.Unmarshal(pin.Body.Bytes(), &pinResult); err != nil || pinResult["action"] != "pin" || pinResult["path"] != "note.md" {
		t.Fatalf("unexpected pin JSON response: %v, err=%v", pinResult, err)
	}
	if _, err := os.Stat(filepath.Join(cfgDirectory, pinStateFile)); err != nil {
		t.Fatalf("pin state was not persisted: %v", err)
	}

	unpin := pinRequest(t, map[string]string{"action": "unpin", "path": "note.md"}, true)
	if unpin.Code != http.StatusOK || isPinned("note.md") {
		t.Fatalf("unpin: status=%d pinned=%t body=%s", unpin.Code, isPinned("note.md"), unpin.Body.String())
	}
}

func TestPinHandlerJSONValidation(t *testing.T) {
	setupPinTest(t)
	for _, tc := range []struct {
		name string
		body map[string]string
		want int
	}{
		{name: "unknown action", body: map[string]string{"action": "delete", "path": "note.md"}, want: http.StatusBadRequest},
		{name: "traversal", body: map[string]string{"action": "pin", "path": "../outside"}, want: http.StatusForbidden},
		{name: "missing file", body: map[string]string{"action": "pin", "path": "missing.md"}, want: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := pinRequest(t, tc.body, true)
			if got.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", got.Code, tc.want, got.Body.String())
			}
			if got.Header().Get("Content-Type") != "application/json; charset=utf-8" {
				t.Fatalf("unexpected content type: %q", got.Header().Get("Content-Type"))
			}
		})
	}
}

func TestPinHandlerJSONRequiresSession(t *testing.T) {
	setupPinTest(t)
	got := pinRequest(t, map[string]string{"action": "pin", "path": "note.md"}, false)
	if got.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want=%d body=%s", got.Code, http.StatusUnauthorized, got.Body.String())
	}
}

func TestPinHandlerAcceptsDedicatedBearerToken(t *testing.T) {
	setupPinTest(t)
	if err := os.WriteFile(filepath.Join(cfgDirectory, "note.md"), []byte("note"), 0600); err != nil {
		t.Fatal(err)
	}
	cfgPinAPIToken = "test-secret-token"
	t.Cleanup(func() { cfgPinAPIToken = "" })
	body := bytes.NewBufferString(`{"action":"pin","path":"note.md"}`)
	req := httptest.NewRequest(http.MethodPost, "/pin", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-secret-token")
	w := httptest.NewRecorder()
	pinHandler(w, req)
	if w.Code != http.StatusOK || !isPinned("note.md") {
		t.Fatalf("status=%d pinned=%t body=%s", w.Code, isPinned("note.md"), w.Body.String())
	}
}

func TestPinsAPIHandlerListsPinsWithBearerToken(t *testing.T) {
	setupPinTest(t)
	cfgPinAPIToken = "test-secret-token"
	t.Cleanup(func() { cfgPinAPIToken = "" })
	pinnedPaths = []string{"files/docs/example.md"}
	req := httptest.NewRequest(http.MethodGet, "/api/pins", nil)
	req.Header.Set("Authorization", "Bearer test-secret-token")
	w := httptest.NewRecorder()
	pinsAPIHandler(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var response struct {
		Pins []string `json:"pins"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || len(response.Pins) != 1 || response.Pins[0] != "files/docs/example.md" {
		t.Fatalf("unexpected pins response: %+v, err=%v", response, err)
	}
}

func TestPinHandlerAcceptsOnlySingleJSONDocument(t *testing.T) {
	setupPinTest(t)
	if err := os.WriteFile(filepath.Join(cfgDirectory, "note.md"), []byte("note"), 0600); err != nil {
		t.Fatal(err)
	}
	body := bytes.NewBufferString(`{"action":"pin","path":"note.md"} {"extra":true}`)
	req := httptest.NewRequest(http.MethodPost, "/pin", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: "workspace_session", Value: createTestSession(t)})
	w := httptest.NewRecorder()
	pinHandler(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want=%d body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

func createTestSession(t *testing.T) string {
	t.Helper()
	session, _ := createSession(false)
	return session
}

func TestPinHandlerRejectsExternalSymlink(t *testing.T) {
	setupPinTest(t)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(cfgDirectory, "escape.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	got := pinRequest(t, map[string]string{"action": "pin", "path": "escape.txt"}, true)
	if got.Code != http.StatusForbidden {
		t.Fatalf("status=%d want=%d body=%s", got.Code, http.StatusForbidden, got.Body.String())
	}
}
