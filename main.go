package main

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
)

//go:embed login.html
var loginHTML string

// Configuration — all values can be overridden via environment variables.
var (
	cfgPort         string
	cfgDirectory    string
	cfgUsername     string
	cfgPassword     string
	cfgSecureCookie bool // set PORTAL_TLS=true when served over HTTPS
)

// Session management — in-memory token store.
var (
	sessionMu sync.RWMutex
	sessions  = make(map[string]time.Time) // token → expiry
)

const (
	sessionDuration       = 24 * time.Hour
	sessionDurationLong   = 30 * 24 * time.Hour
)

func generateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("crypto/rand unavailable: %v", err)
	}
	return base64.URLEncoding.EncodeToString(b)
}

func createSession(remember bool) (string, time.Time) {
	token := generateToken()
	d := sessionDuration
	if remember {
		d = sessionDurationLong
	}
	expiry := time.Now().Add(d)
	sessionMu.Lock()
	sessions[token] = expiry
	sessionMu.Unlock()
	return token, expiry
}

func validateSession(r *http.Request) bool {
	cookie, err := r.Cookie("workspace_session")
	if err != nil {
		return false
	}
	sessionMu.RLock()
	expiry, ok := sessions[cookie.Value]
	sessionMu.RUnlock()
	return ok && time.Now().Before(expiry)
}

func clearSession(r *http.Request) {
	cookie, err := r.Cookie("workspace_session")
	if err != nil {
		return
	}
	sessionMu.Lock()
	delete(sessions, cookie.Value)
	sessionMu.Unlock()
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

var mdRenderer = goldmark.New(
	goldmark.WithExtensions(
		extension.GFM,
		extension.Table,
		extension.Strikethrough,
		extension.TaskList,
		extension.Footnote,
	),
	goldmark.WithParserOptions(
		parser.WithAutoHeadingID(),
	),
)

func main() {
	cfgPort = envOrDefault("PORTAL_PORT", "3000")
	cfgUsername = envOrDefault("PORTAL_USER", "su600")
	cfgPassword = envOrDefault("PORTAL_PASS", "password123")
	cfgSecureCookie = strings.EqualFold(os.Getenv("PORTAL_TLS"), "true")

	rawDir := envOrDefault("PORTAL_DIR", "/root/.openclaw/workspace")
	absDir, err := filepath.Abs(rawDir)
	if err != nil {
		log.Fatalf("cannot resolve PORTAL_DIR: %v", err)
	}
	cfgDirectory = absDir

	// Periodically remove expired sessions to prevent unbounded memory growth.
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			now := time.Now()
			sessionMu.Lock()
			for token, expiry := range sessions {
				if now.After(expiry) {
					delete(sessions, token)
				}
			}
			sessionMu.Unlock()
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/login", loginHandler)
	mux.HandleFunc("/", handler)
	addr := ":" + cfgPort
	log.Printf("🚀 Workspace Portal running at http://localhost%s  dir=%s", addr, cfgDirectory)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}

// loginHandler serves the login page (GET) and processes login form submissions (POST).
func loginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		if validateSession(r) {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, loginHTML)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")
	remember := r.FormValue("remember") == "on"

	userOK := subtle.ConstantTimeCompare([]byte(username), []byte(cfgUsername)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(password), []byte(cfgPassword)) == 1

	if !userOK || !passOK {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		const errDiv = `<div class="error-msg">用户名或密码错误，请重试。</div>`
		page := strings.Replace(loginHTML, `<form method="POST" id="loginForm">`, errDiv+`<form method="POST" id="loginForm">`, 1)
		fmt.Fprint(w, page)
		return
	}

	token, expiry := createSession(remember)
	d := sessionDuration
	if remember {
		d = sessionDurationLong
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "workspace_session",
		Value:    token,
		Path:     "/",
		Expires:  expiry,
		MaxAge:   int(d.Seconds()),
		HttpOnly: true,
		Secure:   cfgSecureCookie,
		SameSite: http.SameSiteLaxMode,
	})

	next := r.URL.Query().Get("next")
	if !isSafeRedirect(next) {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// isSafeRedirect returns true only for relative paths that stay on the same origin.
// It rejects empty strings, scheme-relative URLs like "//evil.com", and any URL
// with a non-empty scheme or host (e.g. "https://evil.com").
func isSafeRedirect(next string) bool {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return false
	}
	parsed, err := url.Parse(next)
	if err != nil {
		return false
	}
	return parsed.Scheme == "" && parsed.Host == ""
}

// isUnder reports whether child is rooted under parent (both must be absolute, cleaned paths).
func isUnder(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

// urlEncodePath encodes a slash-separated relative URL path by percent-encoding each
// individual segment so that "/" separators are preserved.
func urlEncodePath(relPath string) string {
	if relPath == "" {
		return ""
	}
	parts := strings.Split(relPath, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

func handler(w http.ResponseWriter, r *http.Request) {
	// Logout — clear session and redirect to login
	if r.URL.Path == "/logout" {
		clearSession(r)
		http.SetCookie(w, &http.Cookie{
			Name:     "workspace_session",
			Value:    "",
			Path:     "/",
			Expires:  time.Unix(0, 0),
			MaxAge:   -1,
			HttpOnly: true,
			Secure:   cfgSecureCookie,
			SameSite: http.SameSiteLaxMode,
		})
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	// All other endpoints require authentication
	if !validateSession(r) {
		next := r.URL.RequestURI()
		http.Redirect(w, r, "/login?next="+url.QueryEscape(next), http.StatusSeeOther)
		return
	}

	// PWA static resources (served after auth)
	if r.URL.Path == "/manifest.json" {
		http.ServeFile(w, r, filepath.Join(cfgDirectory, "dashboard/manifest_workspace.json"))
		return
	}
	if r.URL.Path == "/sw.js" {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		http.ServeFile(w, r, filepath.Join(cfgDirectory, "dashboard/sw_workspace.js"))
		return
	}

	// Decode and clean the request path into a relative URL path (always "/"-separated)
	relPath := path.Clean(strings.TrimPrefix(r.URL.Path, "/"))
	if relPath == "." {
		relPath = ""
	}
	// Reject any remaining traversal sequences as an early guard
	if strings.HasPrefix(relPath, "../") || relPath == ".." {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	// Resolve to absolute filesystem path and verify it stays inside cfgDirectory
	absPath := filepath.Join(cfgDirectory, filepath.FromSlash(relPath))
	if !isUnder(absPath, cfgDirectory) {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	info, err := os.Stat(absPath)
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	query := r.URL.Query()

	if _, ok := query["download"]; ok {
		serveDownload(w, r, absPath)
		return
	}

	// Handle POST requests for MD file editing
	if _, ok := query["edit"]; ok && r.Method == http.MethodPost {
		if !info.IsDir() && strings.HasSuffix(strings.ToLower(absPath), ".md") {
			saveMarkdown(w, r, absPath, relPath)
			return
		}
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if info.IsDir() {
		listDirectory(w, r, absPath, relPath)
	} else if strings.HasSuffix(strings.ToLower(absPath), ".md") {
		if _, ok := query["raw"]; ok {
			serveFile(w, r, absPath)
		} else {
			renderMarkdown(w, absPath, relPath)
		}
	} else {
		serveFile(w, r, absPath)
	}
}

// ─── Directory listing ────────────────────────────────────────────────────────

type fileEntry struct {
	Name  string
	IsDir bool
	Size  int64
	Mtime time.Time
}

func listDirectory(w http.ResponseWriter, r *http.Request, absPath, relPath string) {
	entries, err := os.ReadDir(absPath)
	if err != nil {
		http.Error(w, "Cannot read directory", http.StatusInternalServerError)
		return
	}

	q := r.URL.Query()
	sortBy := q.Get("sort")
	if sortBy == "" {
		sortBy = "name"
	}
	_, descOrder := q["desc"]

	files := make([]fileEntry, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileEntry{
			Name:  e.Name(),
			IsDir: e.IsDir(),
			Size:  info.Size(),
			Mtime: info.ModTime(),
		})
	}

	// Dirs first, then sort within each group
	sort.SliceStable(files, func(i, j int) bool {
		if files[i].IsDir != files[j].IsDir {
			return files[i].IsDir
		}
		var less bool
		switch sortBy {
		case "mtime":
			less = files[i].Mtime.Before(files[j].Mtime)
		case "size":
			less = files[i].Size < files[j].Size
		default:
			less = strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name)
		}
		if descOrder {
			return !less
		}
		return less
	})

	// Build breadcrumbs
	breadcrumbs := buildBreadcrumbs(relPath)

	// Sort header helpers
	sortLink := func(field string) string {
		if sortBy == field {
			if descOrder {
				return "?" + "sort=" + field
			}
			return "?" + "sort=" + field + "&desc"
		}
		return "?" + "sort=" + field
	}
	sortIndicator := func(field string) string {
		if sortBy == field {
			if descOrder {
				return " ↓"
			}
			return " ↑"
		}
		return ""
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE html><html lang="zh-CN"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>🚀 Workspace — /` + html.EscapeString(relPath) + `</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
:root{--blue:#1a73e8;--blue-dark:#1558b0;--bg:#f0f2f5;--card:#fff;--border:#e8eaed;--text:#202124;--muted:#5f6368;--hover:#f8f9fa}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Oxygen,Ubuntu,sans-serif;background:var(--bg);color:var(--text);min-height:100vh}
a{color:var(--blue);text-decoration:none}
a:hover{text-decoration:underline}

/* Header */
.header{background:linear-gradient(135deg,#1a73e8,#0d47a1);color:#fff;padding:0 16px;height:56px;display:flex;align-items:center;justify-content:space-between;position:sticky;top:0;z-index:100;box-shadow:0 2px 8px rgba(0,0,0,.2)}
.header-left{display:flex;align-items:center;gap:10px;overflow:hidden}
.header-logo{font-size:20px;flex-shrink:0}
.header-title{font-size:15px;font-weight:600;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.header-right{display:flex;align-items:center;gap:8px;flex-shrink:0}
.btn-logout{color:#fff;background:rgba(255,255,255,.2);border:1px solid rgba(255,255,255,.3);padding:5px 12px;border-radius:20px;font-size:12px;cursor:pointer;transition:background .15s}
.btn-logout:hover{background:rgba(255,255,255,.3);text-decoration:none}

/* Breadcrumb */
.breadcrumb{padding:12px 16px 0;display:flex;flex-wrap:wrap;gap:4px;align-items:center;font-size:13px;color:var(--muted)}
.breadcrumb a{color:var(--blue)}
.breadcrumb span{color:var(--muted)}

/* Container */
.container{max-width:1200px;margin:12px auto;padding:0 12px 24px}

/* File table card */
.card{background:var(--card);border-radius:12px;box-shadow:0 1px 4px rgba(0,0,0,.08);overflow:hidden}
.card-header{padding:14px 16px;border-bottom:1px solid var(--border);display:flex;align-items:center;justify-content:space-between;flex-wrap:wrap;gap:8px}
.card-header-title{font-size:14px;font-weight:600;color:var(--muted)}
.file-count{font-size:12px;color:var(--muted);background:#f1f3f4;padding:2px 8px;border-radius:10px}

/* Table */
.file-table{width:100%;border-collapse:collapse}
.file-table th{background:#f8f9fa;padding:10px 16px;text-align:left;font-size:12px;font-weight:600;color:var(--muted);letter-spacing:.5px;text-transform:uppercase;white-space:nowrap;position:sticky;top:56px;z-index:10}
.file-table th a{color:var(--muted);display:inline-flex;align-items:center;gap:2px}
.file-table th a:hover{color:var(--blue);text-decoration:none}
.file-table td{padding:10px 16px;border-top:1px solid var(--border);font-size:14px;vertical-align:middle}
.file-table tr:hover td{background:var(--hover)}
.file-table .col-name{min-width:160px}
.file-table .col-size{width:90px;text-align:right;color:var(--muted)}
.file-table .col-mtime{width:130px;color:var(--muted)}
.file-table .col-action{width:60px;text-align:center}
.file-name{display:flex;align-items:center;gap:8px}
.file-icon{font-size:18px;flex-shrink:0;line-height:1}
.file-link{font-weight:500;color:var(--text)}
.file-link:hover{color:var(--blue)}
.dir-link{color:var(--blue) !important}
.btn-dl{display:inline-flex;align-items:center;gap:3px;padding:4px 10px;border:1px solid var(--border);border-radius:6px;color:var(--muted);font-size:12px;transition:all .15s;background:var(--card)}
.btn-dl:hover{border-color:var(--blue);color:var(--blue);background:#e8f0fe;text-decoration:none}

/* Mobile cards view */
@media(max-width:640px){
  .file-table thead{display:none}
  .file-table,.file-table tbody,.file-table tr,.file-table td{display:block;width:100%}
  .file-table tr{padding:10px 16px;border-top:1px solid var(--border);display:flex;flex-wrap:wrap;gap:4px;align-items:center}
  .file-table tr:hover{background:var(--hover)}
  .file-table td{padding:0;border:none;font-size:13px}
  .file-table .col-name{width:100%;order:1}
  .file-table .col-size{order:2;margin-left:auto;text-align:right}
  .file-table .col-mtime{order:3;font-size:12px}
  .file-table .col-action{order:4;margin-left:8px}
  .header-title{font-size:13px}
  .container{padding:0 8px 24px}
}

/* Empty state */
.empty{text-align:center;padding:48px 24px;color:var(--muted)}
.empty-icon{font-size:48px;margin-bottom:12px}
.empty-text{font-size:14px}
</style>
</head>
<body>
<header class="header">
  <div class="header-left">
    <span class="header-logo">🚀</span>
    <span class="header-title">` + html.EscapeString("/"+relPath) + `</span>
  </div>
  <div class="header-right">
    <a href="/logout" class="btn-logout">退出登录</a>
  </div>
</header>
`)

	// Breadcrumb
	sb.WriteString(`<nav class="breadcrumb">`)
	for i, bc := range breadcrumbs {
		if i > 0 {
			sb.WriteString(`<span>/</span>`)
		}
		if bc.Path == "" {
			sb.WriteString(`<a href="/">🏠 根目录</a>`)
		} else if i < len(breadcrumbs)-1 {
			sb.WriteString(`<a href="/` + urlEncodePath(bc.Path) + `">` + html.EscapeString(bc.Name) + `</a>`)
		} else {
			sb.WriteString(`<span>` + html.EscapeString(bc.Name) + `</span>`)
		}
	}
	sb.WriteString(`</nav>`)

	sb.WriteString(`<div class="container"><div class="card">`)
	sb.WriteString(`<div class="card-header"><span class="card-header-title">📂 文件列表</span><span class="file-count">` + fmt.Sprintf("%d 项", len(files)) + `</span></div>`)
	sb.WriteString(`<table class="file-table"><thead><tr>`)
	sb.WriteString(`<th class="col-name"><a href="` + sortLink("name") + `">名称` + sortIndicator("name") + `</a></th>`)
	sb.WriteString(`<th class="col-size"><a href="` + sortLink("size") + `">大小` + sortIndicator("size") + `</a></th>`)
	sb.WriteString(`<th class="col-mtime"><a href="` + sortLink("mtime") + `">修改时间` + sortIndicator("mtime") + `</a></th>`)
	sb.WriteString(`<th class="col-action">操作</th>`)
	sb.WriteString(`</tr></thead><tbody>`)

	// Parent directory link
	if relPath != "" {
		parent := path.Dir(relPath)
		if parent == "." {
			parent = ""
		}
		var parentHref string
		if parent == "" {
			parentHref = "/"
		} else {
			parentHref = "/" + urlEncodePath(parent)
		}
		sb.WriteString(`<tr><td class="col-name"><div class="file-name"><span class="file-icon">📁</span><a href="` + parentHref + `" class="file-link dir-link">..</a></div></td><td class="col-size">—</td><td class="col-mtime">—</td><td class="col-action"></td></tr>`)
	}

	if len(files) == 0 {
		sb.WriteString(`</tbody></table><div class="empty"><div class="empty-icon">📭</div><div class="empty-text">此目录为空</div></div>`)
	} else {
		for _, f := range files {
			escapedName := html.EscapeString(f.Name)
			var hrefPath string
			if relPath == "" {
				hrefPath = "/" + urlEncodePath(f.Name)
			} else {
				hrefPath = "/" + urlEncodePath(relPath) + "/" + urlEncodePath(f.Name)
			}

			var icon, sizeStr, actionBtn string
			if f.IsDir {
				icon = "📁"
				sizeStr = "—"
				actionBtn = ""
			} else {
				icon = fileIcon(f.Name)
				sizeStr = humanSize(f.Size)
				actionBtn = `<a href="` + hrefPath + `?download=1" class="btn-dl" title="下载">⬇</a>`
			}

			mtimeStr := f.Mtime.Format("01-02 15:04")
			linkClass := "file-link"
			if f.IsDir {
				linkClass += " dir-link"
			}
			target := ""
			if strings.HasSuffix(strings.ToLower(f.Name), ".md") {
				target = ` target="_blank" rel="noopener noreferrer"`
			}

			sb.WriteString(`<tr>`)
			sb.WriteString(`<td class="col-name"><div class="file-name"><span class="file-icon">` + icon + `</span><a href="` + hrefPath + `" class="` + linkClass + `"` + target + `>` + escapedName + `</a></div></td>`)
			sb.WriteString(`<td class="col-size">` + sizeStr + `</td>`)
			sb.WriteString(`<td class="col-mtime">` + mtimeStr + `</td>`)
			sb.WriteString(`<td class="col-action">` + actionBtn + `</td>`)
			sb.WriteString(`</tr>`)
		}
		sb.WriteString(`</tbody></table>`)
	}

	sb.WriteString(`</div></div></body></html>`)
	fmt.Fprint(w, sb.String())
}

// ─── Markdown rendering ───────────────────────────────────────────────────────

func renderMarkdown(w http.ResponseWriter, absPath, relPath string) {
	content, err := os.ReadFile(absPath)
	if err != nil {
		http.Error(w, "Cannot read file", http.StatusInternalServerError)
		return
	}

	var rendered bytes.Buffer
	if err := mdRenderer.Convert(content, &rendered); err != nil {
		http.Error(w, "Cannot render markdown", http.StatusInternalServerError)
		return
	}

	// Build parent URL for back link (relPath uses "/" as separator)
	parent := path.Dir(relPath)
	if parent == "." {
		parent = ""
	}
	var parentURL string
	if parent == "" {
		parentURL = "/"
	} else {
		parentURL = "/" + urlEncodePath(parent)
	}

	// Build the form action URL for saving edits
	var editActionURL string
	if relPath == "" {
		editActionURL = "/?edit"
	} else {
		editActionURL = "/" + urlEncodePath(relPath) + "?edit"
	}

	fileName := filepath.Base(relPath)
	escapedSource := html.EscapeString(string(content))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	fmt.Fprintf(w, `<!DOCTYPE html><html lang="zh-CN"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>%s — Workspace</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
:root{--blue:#1a73e8;--bg:#f0f2f5;--card:#fff;--border:#e8eaed;--text:#202124;--muted:#5f6368;--code-bg:#f6f8fa}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Oxygen,Ubuntu,sans-serif;background:var(--bg);color:var(--text);min-height:100vh}
a{color:var(--blue);text-decoration:none}
a:hover{text-decoration:underline}

/* Header */
.header{background:linear-gradient(135deg,#1a73e8,#0d47a1);color:#fff;padding:0 16px;height:56px;display:flex;align-items:center;justify-content:space-between;position:sticky;top:0;z-index:100;box-shadow:0 2px 8px rgba(0,0,0,.2)}
.header-left{display:flex;align-items:center;gap:10px;overflow:hidden}
.header-logo{font-size:20px;flex-shrink:0}
.header-title{font-size:14px;font-weight:600;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;opacity:.9}
.header-right{display:flex;align-items:center;gap:8px;flex-shrink:0}
.btn-back{color:#fff;background:rgba(255,255,255,.2);border:1px solid rgba(255,255,255,.3);padding:5px 12px;border-radius:20px;font-size:12px;transition:background .15s;white-space:nowrap}
.btn-back:hover{background:rgba(255,255,255,.3);text-decoration:none}
.btn-logout{color:rgba(255,255,255,.8);font-size:12px;padding:5px 10px;border-radius:20px;transition:background .15s}
.btn-logout:hover{background:rgba(255,255,255,.15);text-decoration:none;color:#fff}
.btn-edit{color:#fff;background:rgba(255,255,255,.2);border:1px solid rgba(255,255,255,.3);padding:5px 12px;border-radius:20px;font-size:12px;cursor:pointer;transition:background .15s;white-space:nowrap}
.btn-edit:hover{background:rgba(255,255,255,.3)}
.btn-save{color:#fff;background:#1e8a3c;border:1px solid rgba(255,255,255,.3);padding:5px 12px;border-radius:20px;font-size:12px;cursor:pointer;transition:background .15s;white-space:nowrap}
.btn-save:hover{background:#176b2f}
.btn-cancel{color:#fff;background:rgba(255,255,255,.2);border:1px solid rgba(255,255,255,.3);padding:5px 12px;border-radius:20px;font-size:12px;cursor:pointer;transition:background .15s;white-space:nowrap}
.btn-cancel:hover{background:rgba(255,255,255,.3)}

/* Container */
.wrapper{max-width:860px;margin:24px auto;padding:0 16px 48px}
.md-card{background:var(--card);border-radius:12px;box-shadow:0 1px 4px rgba(0,0,0,.08);padding:32px 40px}

/* Markdown content */
.md-body{font-size:15px;line-height:1.75;color:#24292f}
.md-body h1{font-size:1.9em;border-bottom:2px solid var(--border);padding-bottom:12px;margin:0 0 20px}
.md-body h2{font-size:1.5em;border-bottom:1px solid var(--border);padding-bottom:8px;margin:32px 0 16px}
.md-body h3{font-size:1.2em;margin:24px 0 12px}
.md-body h4{font-size:1.05em;margin:20px 0 8px}
.md-body h5,.md-body h6{font-size:.95em;margin:16px 0 8px;color:var(--muted)}
.md-body p{margin:12px 0}
.md-body ul,.md-body ol{padding-left:28px;margin:12px 0}
.md-body li{margin:6px 0}
.md-body li p{margin:4px 0}
.md-body code{background:var(--code-bg);border:1px solid var(--border);padding:2px 6px;border-radius:4px;font-family:"SFMono-Regular",Consolas,"Liberation Mono",Menlo,monospace;font-size:.875em;color:#d63384}
.md-body pre{background:var(--code-bg);border:1px solid var(--border);border-left:4px solid var(--blue);border-radius:6px;padding:16px;overflow-x:auto;margin:16px 0;line-height:1.5}
.md-body pre code{background:none;border:none;padding:0;color:#24292f;font-size:.875em;word-break:normal}
.md-body blockquote{border-left:4px solid #d0d7de;margin:16px 0;padding:8px 16px;color:var(--muted);background:#f6f8fa;border-radius:0 6px 6px 0}
.md-body blockquote p{margin:4px 0}
.md-body table{border-collapse:collapse;margin:16px 0;width:100%%;overflow-x:auto;display:block}
.md-body th,.md-body td{border:1px solid var(--border);padding:8px 14px;text-align:left}
.md-body th{background:#f6f8fa;font-weight:600}
.md-body tr:nth-child(even) td{background:#fafafa}
.md-body hr{border:none;border-top:2px solid var(--border);margin:24px 0}
.md-body img{max-width:100%%;border-radius:6px;margin:8px 0}
.md-body a{color:var(--blue)}
.md-body a:hover{text-decoration:underline}
/* Task list checkboxes */
.md-body .contains-task-list{list-style:none;padding-left:4px}
.md-body .task-list-item{display:flex;align-items:flex-start;gap:8px}
.md-body .task-list-item input{margin-top:4px;flex-shrink:0}

/* Editor */
.edit-card{background:var(--card);border-radius:12px;box-shadow:0 1px 4px rgba(0,0,0,.08);display:none;flex-direction:column}
.md-editor{width:100%%;min-height:60vh;padding:20px;font-family:"SFMono-Regular",Consolas,"Liberation Mono",Menlo,monospace;font-size:14px;line-height:1.6;border:none;outline:none;resize:vertical;border-radius:12px;color:var(--text);background:var(--card)}

@media(max-width:640px){
  .wrapper{padding:0 8px 32px;margin:12px auto}
  .md-card{padding:20px 16px;border-radius:8px}
  .md-body{font-size:14px}
  .md-body h1{font-size:1.6em}
  .md-body h2{font-size:1.3em}
  .header-title{display:none}
}
</style>
</head>
<body>
<header class="header">
  <div class="header-left">
    <span class="header-logo">📄</span>
    <span class="header-title">%s</span>
  </div>
  <div class="header-right">
    <a href="%s" id="btn-back" class="btn-back">← 返回</a>
    <button id="btn-edit" class="btn-edit" onclick="startEdit()">✏️ 编辑</button>
    <button id="btn-save" class="btn-save" style="display:none" onclick="document.getElementById('edit-form').submit()">💾 保存</button>
    <button id="btn-cancel" class="btn-cancel" style="display:none" onclick="cancelEdit()">✕ 取消</button>
    <a href="/logout" class="btn-logout">退出</a>
  </div>
</header>
<div class="wrapper">
  <div id="preview-card" class="md-card">
    <article class="md-body">
%s
    </article>
  </div>
  <form id="edit-form" method="POST" action="%s" class="edit-card">
    <textarea id="md-editor" name="content" class="md-editor" spellcheck="false">%s</textarea>
  </form>
</div>
<script>
function startEdit(){
  document.getElementById('preview-card').style.display='none';
  var ef=document.getElementById('edit-form');
  ef.style.display='flex';
  document.getElementById('md-editor').focus();
  document.getElementById('btn-edit').style.display='none';
  document.getElementById('btn-back').style.display='none';
  document.getElementById('btn-save').style.display='';
  document.getElementById('btn-cancel').style.display='';
}
function cancelEdit(){
  document.getElementById('preview-card').style.display='';
  document.getElementById('edit-form').style.display='none';
  document.getElementById('btn-edit').style.display='';
  document.getElementById('btn-back').style.display='';
  document.getElementById('btn-save').style.display='none';
  document.getElementById('btn-cancel').style.display='none';
}
</script>
</body></html>`,
		html.EscapeString(fileName),
		html.EscapeString(fileName),
		parentURL,
		rendered.String(),
		editActionURL,
		escapedSource,
	)
}

// ─── File serving ─────────────────────────────────────────────────────────────

func serveFile(w http.ResponseWriter, r *http.Request, absPath string) {
	ext := strings.ToLower(filepath.Ext(absPath))
	mimeType := mime.TypeByExtension(ext)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mimeType)
	http.ServeFile(w, r, absPath)
}

func serveDownload(w http.ResponseWriter, r *http.Request, absPath string) {
	fileName := url.PathEscape(filepath.Base(absPath))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename*=UTF-8''%s`, fileName))
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, absPath)
}

// maxMarkdownEditBytes caps the size of an edited markdown file at 10 MiB.
const maxMarkdownEditBytes = 10 << 20

// saveMarkdown writes the edited markdown content back to disk atomically, then
// redirects to the preview page.
func saveMarkdown(w http.ResponseWriter, r *http.Request, absPath, relPath string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxMarkdownEditBytes)
	if err := r.ParseForm(); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			http.Error(w, "Content too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "Bad request", http.StatusBadRequest)
		}
		return
	}
	content := r.FormValue("content")
	if int64(len(content)) > maxMarkdownEditBytes {
		http.Error(w, "Content too large", http.StatusRequestEntityTooLarge)
		return
	}

	// Use Lstat so we see the symlink itself rather than its target; reject symlinks
	// to prevent writing outside cfgDirectory via a symlink planted inside it.
	info, err := os.Lstat(absPath)
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}
	if info.Mode()&os.ModeSymlink != 0 {
		http.Error(w, "Cannot save file", http.StatusBadRequest)
		return
	}

	// Atomic write: write to a sibling temp file, sync, then rename into place.
	dir := filepath.Dir(absPath)
	tmp, err := os.CreateTemp(dir, ".md-edit-*")
	if err != nil {
		http.Error(w, "Cannot save file", http.StatusInternalServerError)
		return
	}
	tmpName := tmp.Name()
	// Ensure temp file is cleaned up on any early exit.
	committed := false
	defer func() {
		if !committed {
			os.Remove(tmpName)
		}
	}()
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		http.Error(w, "Cannot save file", http.StatusInternalServerError)
		return
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		http.Error(w, "Cannot save file", http.StatusInternalServerError)
		return
	}
	if err := tmp.Chmod(info.Mode()); err != nil {
		tmp.Close()
		http.Error(w, "Cannot save file", http.StatusInternalServerError)
		return
	}
	tmp.Close()
	if err := os.Rename(tmpName, absPath); err != nil {
		http.Error(w, "Cannot save file", http.StatusInternalServerError)
		return
	}
	committed = true

	var redirectURL string
	if relPath == "" {
		redirectURL = "/"
	} else {
		redirectURL = "/" + urlEncodePath(relPath)
	}
	http.Redirect(w, r, redirectURL, http.StatusSeeOther)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

type breadcrumb struct {
	Name string
	Path string
}

func buildBreadcrumbs(relPath string) []breadcrumb {
	bcs := []breadcrumb{{Name: "根目录", Path: ""}}
	if relPath == "" {
		return bcs
	}
	parts := strings.Split(relPath, "/")
	for i, p := range parts {
		if p == "" {
			continue
		}
		bcs = append(bcs, breadcrumb{
			Name: p,
			Path: strings.Join(parts[:i+1], "/"),
		})
	}
	return bcs
}

func humanSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func fileIcon(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".md", ".markdown", ".txt", ".rst":
		return "📝"
	case ".pdf":
		return "📕"
	case ".jpg", ".jpeg", ".png", ".gif", ".svg", ".webp", ".ico", ".bmp":
		return "🖼️"
	case ".mp4", ".avi", ".mov", ".mkv", ".webm":
		return "🎬"
	case ".mp3", ".wav", ".ogg", ".flac", ".aac":
		return "🎵"
	case ".zip", ".tar", ".gz", ".bz2", ".xz", ".7z", ".rar":
		return "📦"
	case ".go":
		return "🐹"
	case ".py":
		return "🐍"
	case ".js", ".ts", ".jsx", ".tsx":
		return "📜"
	case ".html", ".htm":
		return "🌐"
	case ".css":
		return "🎨"
	case ".json", ".yaml", ".yml", ".toml", ".ini", ".cfg", ".conf":
		return "⚙️"
	case ".sh", ".bash", ".zsh", ".fish":
		return "💻"
	case ".sql":
		return "🗄️"
	case ".xml":
		return "📋"
	case ".log":
		return "📃"
	case ".key", ".pem", ".crt", ".cert":
		return "🔑"
	default:
		return "📄"
	}
}
