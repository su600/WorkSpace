package main

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func uploadRequest(t *testing.T, filename string, contents []byte, username, password string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(contents); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if username != "" || password != "" {
		req.SetBasicAuth(username, password)
	}
	w := httptest.NewRecorder()
	uploadAPIHandler(w, req)
	return w
}

func setupUploadTest(t *testing.T) {
	t.Helper()
	oldDir, oldUser, oldPass := cfgDirectory, cfgUsername, cfgPassword
	cfgDirectory = t.TempDir()
	cfgUsername, cfgPassword = "test-user", "test-password"
	t.Cleanup(func() { cfgDirectory, cfgUsername, cfgPassword = oldDir, oldUser, oldPass })
}

func TestUploadAPIStoresFileInFixedTransferFolder(t *testing.T) {
	setupUploadTest(t)
	got := uploadRequest(t, "photo.jpg", []byte("image-bytes"), "test-user", "test-password")
	if got.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", got.Code, got.Body.String())
	}
	data, err := os.ReadFile(filepath.Join(cfgDirectory, uploadDirName, "photo.jpg"))
	if err != nil || string(data) != "image-bytes" {
		t.Fatalf("uploaded content=%q err=%v", data, err)
	}
	if !strings.Contains(got.Body.String(), `"path":"文件中转站/photo.jpg"`) {
		t.Fatalf("unexpected response: %s", got.Body.String())
	}
}

func TestUploadAPIRequiresCredentialsAndRejectsWrongCredentials(t *testing.T) {
	setupUploadTest(t)
	for _, credentials := range [][2]string{{"", ""}, {"test-user", "wrong"}} {
		got := uploadRequest(t, "photo.jpg", []byte("data"), credentials[0], credentials[1])
		if got.Code != http.StatusUnauthorized {
			t.Fatalf("credentials %q: status=%d body=%s", credentials, got.Code, got.Body.String())
		}
	}
}

func TestUploadAPISanitizesFilenameAndDoesNotOverwrite(t *testing.T) {
	setupUploadTest(t)
	got := uploadRequest(t, `C:\\Users\\user\\note.txt`, []byte("first"), "test-user", "test-password")
	if got.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", got.Code, got.Body.String())
	}
	got = uploadRequest(t, "note.txt", []byte("second"), "test-user", "test-password")
	if got.Code != http.StatusConflict {
		t.Fatalf("duplicate status=%d body=%s", got.Code, got.Body.String())
	}
	data, err := os.ReadFile(filepath.Join(cfgDirectory, uploadDirName, "note.txt"))
	if err != nil || string(data) != "first" {
		t.Fatalf("existing content=%q err=%v", data, err)
	}
}

func TestUploadAPIMethodNotAllowed(t *testing.T) {
	setupUploadTest(t)
	req := httptest.NewRequest(http.MethodGet, "/api/upload", nil)
	req.SetBasicAuth("test-user", "test-password")
	w := httptest.NewRecorder()
	uploadAPIHandler(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d want=%d", w.Code, http.StatusMethodNotAllowed)
	}
}
