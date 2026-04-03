package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
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
	_ "time/tzdata"

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
	sessionDuration     = 24 * time.Hour
	sessionDurationLong = 30 * 24 * time.Hour
	mtimeFormat         = "01-02 15:04"
	searchMaxResults    = 500
	pinStateFile        = ".workspace_pins.json"
)

// Pin storage — ordered list of pinned relative paths, persisted to disk.
var (
	pinMu       sync.RWMutex
	pinnedPaths []string
)

var mtimeLocation = time.FixedZone("Asia/Shanghai", 8*3600)

// faviconSVG is the inline SVG icon served at /favicon.svg and referenced by all pages.
const faviconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><rect width="32" height="32" rx="6" fill="#1a73e8"/><text x="16" y="22" text-anchor="middle" font-family="sans-serif" font-size="18" font-weight="bold" fill="white">W</text></svg>`

// touchIconPNG is a base64-encoded 180×180 PNG served at /apple-touch-icon.png.
// It contains the same "W" monogram on #1a73e8 blue, which iOS Safari uses for
// home-screen and PWA icons (SVG is not supported there).
const touchIconPNG = "iVBORw0KGgoAAAANSUhEUgAAALQAAAC0CAIAAACyr5FlAAABoElEQVR42u3SwQ0AIAgEQftvGivwQzRBmG1AucxakiRJkiRJkiRJkiRJkiRJkiRJkiRJkiRJkqoX55wMBxxwWAoOSznZUk62lJMt5WRLOdlSTraUk+FwMhxwwGEpOCzlZEs52VJOtpSTLeVkSznZUk62lJPhgENwwGEpJ1vKyZZysqWcbCknW8rJlnKypZwMBxxwwAGHpeCwlJMt5eRmS+Xeuv5DOOCAAw444IADDjjggAMOOOCAAw444IBDcMABBxxwwAEHHHDAAQcccMABBxxwwAEHHHDAAQcccMABBxxwwAEHHHDAAcdwHAO1wQEHHHAIDjjggAMOOOCAAw444IADDjjggAMOOOCAQ3DAAQcccMABBxxwwPEjjvr7Rioe4IADDjjggAMOOOCAAw444IADDjjggAMOOAQHHHDAAQcccMABBxxwwAEHHM1wdB0RDjjggAMOOOCAAw444IADDjjggAMOOOAQHHDAAQcccMABBxxwwAEHHHDAAcccHMaBAw7BITgEh+AQHIJDcEiSJEmSJEmSJEmSJEmSJEmSpNdtNVeqiN/OUH4AAAAASUVORK5CYII="

// icon192PNG is a base64-encoded 192×192 PNG icon required by Android PWA.
const icon192PNG = "iVBORw0KGgoAAAANSUhEUgAAAMAAAADACAYAAABS3GwHAAADBUlEQVR42u3bwRHCMAxEUdVBmzRNFzAc0kGMx/yXmb1bK71jZjZ/j+frLd1M4bNoSeGwPEmCsCRJQrAUSUKwBMlCULxkEShckgiULFkIipUsAoVKFoEiJY1AiZIFoEDJIlCcZBEoTNIIlCVZAIqSNAIlSRaAgiSNQDmSBaAYSSNQigAgAoBIDIBCJI1AGQKACAAiACzPnZ9F7svpewRAAABA0gAUJ+U9AiAAKE4AUJwAoDgBQHECgOIEAMUJAIoTABQnAChOAFCcAKA4AQAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAADgNgRgAchxkBcBxmBMBxmBEAx2FGAByHGQFwHGYEwHGYEQDHYUYAHIcZAXAcZgTAcZgRAMdhRgAchxkBcBxmBMBxmBEAx2FGAByHGQFwHGYEYFFx3gUAAN4FAADeBQAA3gUAAN4FAADeBYCFehcAFupdAFiodwFgod4FgIV6FwAW6l0AWKh3AWCh3gWAhXoXABbqXQBYqHcBYKHeBYCFehcAi4r7hyXoHgAAdA8AALoHAADdAwCA7gEAQPcAAKB7AADQPQAA6B4AAHQPAAC6BwAA3QMAgO4BAED3AACgewAA0D0AAOgeAAB0DwAAuvdPsNgjAGKPAIg9AiD2CIDYIwBijwAIAIoTABQnAChOAFCcAKA4AUBxAoDiBADFCQCKEwAUJwAoTgAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAADgxwC+n+KkuMe5PsUJACKxACAAACAAKEMAEIkCgEDSxw+AAKAUAUAkCgACSR8/AJIHAIGkjx8AyQOAQNLHD4DkAUAg6eOHQPLHD4Hkjx8AyQOAQNLHD4Hkjx8CyR8/CJI/fAjE8UMgjh8EcfggiMMHQRw+EOLg4ZDzjvwDELyg+eSS48AAAAAASUVORK5CYII="

// icon512PNG is a base64-encoded 512×512 PNG icon required by Android PWA.
const icon512PNG = "iVBORw0KGgoAAAANSUhEUgAAAgAAAAIACAYAAAD0eNT6AAAQiklEQVR42u3WgQmDUBBEQeuwTZtOF4olBL6g90aYBnJ/N7ttvuXffvxOANbxz+LzZw6A0eDzZw+AUeDzhw+AQeDzpw+AMeBPHwCMAX/6AGAM+OMHAEPAnz4AGAL++AHAEPDHDwCGgD9/AIwAnz9+AAwBnz9+AAwBnz9/AIwAnz9+AAwBnz9/AIwAf/wAYAj48wcAI8CfPwAYAf78AcAI8OcPAEaAP34AMAT8+QOAEeDPHwCMAH/+AGAE+PMHACPAnz8AGAH+/AHACDAAAKA6ABwMAGIjwKEAIDYCHAgAYiPAYQAgNgIcBACCI8AxACA2ABwCAGIjwAEAIDgC/PgAEBsAfngAiI0APzgABEeAHxsAYgPADw0AwRHgRwaA2ADwAwNAbAT4YQEgOAL8qAAQGwB+UAAIjgA/JgDEBoAfEgCCI8CPCAAGAAAwfQD4AQEgOAL8eAAQGwB+OAAIjgA/GgAYAADA9AHgBwOA4AjwYwGAAQAATB8AfigACI4APxIAGAAAwPQB4AcCgOAI8OMAgAEAABgAAMC4AeCHAYDgCPCjAIABAAAYAACAAQAAfH8A+EEAIDgC/BgAYAAAAAYAAGAAAAAGQNGkzz1Bj+kxAwDBAfSYHjMAEBxAj+kxAwDBAfSYHjMABEdwAD2mxwwAwREcQI/pMQNAcAQH0GN6zAAQHMEB9Jgee9cA8EMIDqDH9FhwBPgRBAfQY3rMAEBwAD2mxwwABAfQY3rMAEBwAD2mxwwAwREcQI/pMQNAcAQH0GN6zAAQHMEB9BgGgOAIDqDHMAAER3AAPYYBIDiCA3pMjxkAfgjBAfSYHjMAEBxAj+kxAwDBAfSYHjMAEBxAj+kxA0BwBAfQY3rMABAcwQH0mB4zAARHcAA9pscMAMERHECP6TEDQHAEB9BjeswAEBzBAfSYHjMABEdwAD2mxwwAwREcQI/pMQNAcAQH0GN6zAAQHMEB9BgGgOAIDqDHMAAER3AAPYYBIDiCA3pMjxkAfgTBAfSYHjMAEBxAj+kxAwDBAfSYHjMAEBxAj+kxA0BwBAfQY3rMABAcwQH0mB4zAARHcAA9pscMAMERHLwxvDFvzAAQHMHBG8Mb88YMAMERHLwxvDFvzAAQHMHxxrwxvDFvzAAQHMHxxrwxvDFvzAAQHMHxxrwxvDFvzAAQHMHxxrwxvDEMAMERHG/MG8MbwwAQHMHxxrwxvDEMAMERHG/MG8MbwwAQHPf0xrwxb8wbMwAQHLwxb8wb88YMAAQHb8wb88a8MQMAwcEb88a8MW/MAEBw8Ma8MW/MGzMAEBy8MW/MG/PGDADBARy8MW/MG/PGDADBARy8MW/MG/PGDADBARy8MW/MG/PGDADBARy8MW/MG/PGDADBARy8Mbwxb8wAEBzBwRvDG/PGDADBERy8Mbwxb8wAEBzBwRvDG/PGDADBERxvzBvDG/PGDADBERxvzBvDG/PGDADBERxvzBvDG3NPA0BwBMcb88bwxjAABEdwvDFvDG8MA0BwBMcb88bwxjAABEdwvDFvzBvzxgwABAdvzBvzxrwxAwDBwRvzxrwxb8wAQHDwxrwxb8wbMwAER3Dwxrwxb8wbMwAER3Dwxrwxb8wbMwAER3Dwxrwxb8wbMwAER3DcxV3cxV3cxQAQHMFxF3dxF3dxFwNAcATHXdzFXdzFXQwAwREcd3EXd3EXA8AAEBzBcRd3cRd3MQAMAMFxF3dxF3dxFwPAAPApAWTfJ/sGgBLwKQFk3yf7BoAS8CkBZN8n+waAEvApAWTfJ/sGgBLwKQFk3yf7BoAS8CkBZN8n+waAEvApAWTfJ/sGgBLwKQFkX/YxAJSAEgDZl30MACWgBED2ZR8DQAkoAZB92ccAUAJKAGRf9g0AlIASANmXfQMAJaAEQPZl3wBACSgBZN8n+waAEvApAWTfJ/sGgBLwKQFk3yf7BoAS8CkBZN8n+waAEvApAWTfJ/sGgBLwKQFk3yf7BoAS8CkBZN8n+waAEvApAWTfJ/sGgBLwKQFk3yf7BoAS8CkBZN8n+waAEvApAWRf9r1NA0AJKAGQfdnHAFACSgBkX/YxAJSAEgDZl30MACWgBED2Zd8AQAkoAZB92TcAUAJKAGRf9g0AlIASANmXfQNACfiUALLvk30DQAn4lACy75N9A0AJ+JQAsu+TfQNACfiUALLvk30DQAn4lACy75N9A0AJ+JQAsu+TfQNACfiUALLvk30DQAn4lACy75N9A0AJ+JQAsu+TfQNACfiUALLvk30DQAkoAW8T2Zd9DAAloARA9mUfA0AJKAGQfdnHAFACSgBkX/YNAD+EElACIPuybwCgBJQAyL7sGwAoASUAsi/7BgBKQAkg+z7ZNwCUgE8JIPs+2TcAlIDgAHpMjxkAgiM4gB7TYwaA4AgOoMf0mAEgOIID6DE9ZgAIjuAAekyPGQCCIziAHtNjBoDgCA6gx/SYASA4ggPoMT1mAAiO4AB6TI8ZAIIjOIAe02MGgOAIDqDH9JgBIDiCA+gxPWYACI7gAHpMjxkAgiM4gB7TYwaA4AgOoMf0mAEgOIID6DE9ZgAIjuAAekyPGQCCIziAHtNjBoDgCA6gx/SYASA4ggPoMT1mAAiO4AB6TI8ZAIIjOIAe02MGgOAIDqDH9JgBIDiCA+gxPWYACI7gAHpMjxkAgiM4gB7jrwFwf34IwQH0mB6L/fkbAIID6DE9ZgAgOIAe02MGAIID6DE9ZgAgOIAe02MGgOAIDqDH9JgBIDiCA+gxPWYAAAAGAABgAAAABgAAYAAAAAYAALB+ABgBABD88zcAAMAAAAAMAADAAAAA5gwAIwAAgn/+BgAAGAAAgAEAAIwdAEYAAAT//A0AADAAAIDKADACACD4528AAIABAABUBoARAADBP38DAAAMAACgMgCMAAAI/vkbAQAQ/fM3AAAgOgCMAAAI/vkbAAAQHQBGAAAE//wNAAAwAACAygAwAgAg+OdvAABAdAAYAQAQ/PM3AAAgOgCMAAAI/vkbAAAQHQBGAAAE//yNAACI/vkbAAAQHQBGAAAE//yNAACI/vkbAAAQHQBGAAAE//yNAACI/vkbAQAQ/fM3AgAg+udvBABA9M/fAACA6AAwAgAg+OdvBABA9M/fCACA6J+/EQAA0T9/IwAAon/+RgAARP/8DQEAiP7xGwEAEP7zNwIA8Ocf/zwEAPz5GwEA4M/fEAAAf/xGAAD48zcEAMAfvxEAAP78DQEA8MdvCACAP34jAAD8+RsCAOCP3xAAAH/8xgAA/vR9hgAA/vh9xgAA/vR9xgAA/vR9xgAA/vR9BgEA/vB9RgEA/ux9RgMA/syf/y5ke45AttoZAQAAAABJRU5ErkJggg=="

// manifestJSON is the embedded PWA web app manifest served at /manifest.json.
// It declares 192×192 and 512×512 PNG icons with purpose "any maskable" so that
// Android Chrome recognises the app as a proper installable PWA rather than a bookmark.
const manifestJSON = `{
  "name": "WorkSpace",
  "short_name": "WorkSpace",
  "description": "WorkSpace 文件管理门户",
  "start_url": "/?source=pwa",
  "display": "standalone",
  "background_color": "#1a73e8",
  "theme_color": "#1a73e8",
  "orientation": "portrait",
  "icons": [
    {
      "src": "/favicon.svg",
      "type": "image/svg+xml",
      "sizes": "any"
    },
    {
      "src": "/icon-192.png",
      "type": "image/png",
      "sizes": "192x192",
      "purpose": "any maskable"
    },
    {
      "src": "/icon-512.png",
      "type": "image/png",
      "sizes": "512x512",
      "purpose": "any maskable"
    }
  ]
}`

// serviceWorkerJS is the embedded service worker script served at /sw.js.
// It pre-caches a small set of public app-shell assets on install and serves
// cached responses for those assets on network failure.  Dynamic and authenticated
// pages are never stored in the cache to avoid leaking user content.
const serviceWorkerJS = `const CACHE = 'ws-v1';
const APP_SHELL = [
  '/login',
  '/manifest.json',
  '/sw.js',
  '/favicon.svg',
  '/icon-192.png',
  '/icon-512.png',
  '/apple-touch-icon.png',
];

self.addEventListener('install', event =>{
  event.waitUntil(caches.open(CACHE).then(cache => cache.addAll(APP_SHELL)));
  self.skipWaiting();
});

self.addEventListener('activate', event => {
  event.waitUntil(
    caches.keys().then(keys =>
      Promise.all(keys.filter(k => k !== CACHE).map(k => caches.delete(k)))
    )
  );
  self.clients.claim();
});

self.addEventListener('fetch', event => {
  if (event.request.method !== 'GET') return;

  const url = new URL(event.request.url);
  if (url.origin !== self.location.origin) return;

  const isShell = APP_SHELL.includes(url.pathname);

  if (!isShell) {
    // Dynamic / authenticated content: network-only. On failure, serve a previously
    // cached version if one exists (e.g., a cached redirect to /login). These responses
    // are not proactively written to the cache by this service worker.
    event.respondWith(
      fetch(event.request).catch(() => caches.match(event.request))
    );
    return;
  }

  // App-shell assets: network-first, cache only safe public responses.
  event.respondWith(
    fetch(event.request)
      .then(response => {
        const cc = response.headers.get('Cache-Control') || '';
        if (response.ok && response.type === 'basic' && !/no-store|private/i.test(cc)) {
          caches.open(CACHE).then(cache => cache.put(event.request, response.clone()));
        }
        return response;
      })
      .catch(() => caches.match(event.request))
  );
});
`

// Decoded PNG bytes for each embedded PNG icon, populated once in init().
var (
	touchIconBytes []byte
	icon192Bytes   []byte
	icon512Bytes   []byte
)

func init() {
	if loc, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		mtimeLocation = loc
	} else {
		log.Printf("warning: cannot load Asia/Shanghai timezone, falling back to +08: %v", err)
	}

	var err error
	if touchIconBytes, err = base64.StdEncoding.DecodeString(touchIconPNG); err != nil {
		log.Fatalf("failed to decode touchIconPNG: %v", err)
	}
	if icon192Bytes, err = base64.StdEncoding.DecodeString(icon192PNG); err != nil {
		log.Fatalf("failed to decode icon192PNG: %v", err)
	}
	if icon512Bytes, err = base64.StdEncoding.DecodeString(icon512PNG); err != nil {
		log.Fatalf("failed to decode icon512PNG: %v", err)
	}
}

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

// ─── Pin helpers ──────────────────────────────────────────────────────────────

func loadPins() {
	p := filepath.Join(cfgDirectory, pinStateFile)
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return // file may not exist yet
		}
		log.Printf("pin: read error: %v", err)
		return
	}
	var raw []string
	if err := json.Unmarshal(data, &raw); err != nil {
		log.Printf("pin: cannot parse pin file: %v", err)
		return
	}
	// Normalize, validate, and deduplicate paths loaded from disk.
	seen := make(map[string]struct{}, len(raw))
	valid := make([]string, 0, len(raw))
	for _, rp := range raw {
		// Normalize using filepath so Windows absolute paths are also caught.
		cleaned := filepath.ToSlash(filepath.Clean(filepath.FromSlash(rp)))
		// Reject absolute paths and paths that escape the root.
		if filepath.IsAbs(filepath.FromSlash(cleaned)) || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
			log.Printf("pin: discarding invalid path from pin file: %q", rp)
			continue
		}
		// Re-validate containment using isUnder (resolves via filepath.Rel).
		abs := filepath.Join(cfgDirectory, filepath.FromSlash(cleaned))
		if !isUnder(cfgDirectory, abs) {
			log.Printf("pin: discarding out-of-root path from pin file: %q", rp)
			continue
		}
		if _, dup := seen[cleaned]; dup {
			continue
		}
		seen[cleaned] = struct{}{}
		valid = append(valid, cleaned)
	}
	pinMu.Lock()
	pinnedPaths = valid
	pinMu.Unlock()
}

func savePins() {
	pinMu.RLock()
	pathsCopy := append([]string(nil), pinnedPaths...)
	pinMu.RUnlock()
	data, err := json.Marshal(pathsCopy)
	if err != nil {
		log.Printf("pin: marshal error: %v", err)
		return
	}
	p := filepath.Join(cfgDirectory, pinStateFile)

	// Write pins atomically: temp file + fsync + rename.
	tmpFile, err := os.CreateTemp(cfgDirectory, pinStateFile+".tmp-*")
	if err != nil {
		log.Printf("pin: cannot create temp file: %v", err)
		return
	}
	tmpName := tmpFile.Name()
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmpName)
		}
	}()

	// Ensure permissions are as expected (0o600).
	if err := os.Chmod(tmpName, 0o600); err != nil {
		log.Printf("pin: chmod temp file error: %v", err)
		_ = tmpFile.Close()
		return
	}

	if _, err := tmpFile.Write(data); err != nil {
		log.Printf("pin: write temp file error: %v", err)
		_ = tmpFile.Close()
		return
	}
	if err := tmpFile.Sync(); err != nil {
		log.Printf("pin: fsync temp file error: %v", err)
		_ = tmpFile.Close()
		return
	}
	if err := tmpFile.Close(); err != nil {
		log.Printf("pin: close temp file error: %v", err)
		return
	}

	if err := os.Rename(tmpName, p); err != nil {
		log.Printf("pin: rename temp file error: %v", err)
		return
	}
	renamed = true

	// Best-effort fsync of directory to persist the rename.
	dirPath := filepath.Dir(p)
	if dirFile, err := os.Open(dirPath); err == nil {
		if err := dirFile.Sync(); err != nil {
			log.Printf("pin: fsync dir error: %v", err)
		}
		_ = dirFile.Close()
	} else {
		log.Printf("pin: open dir for fsync error: %v", err)
	}
}

func isPinned(relPath string) bool {
	pinMu.RLock()
	defer pinMu.RUnlock()
	for _, p := range pinnedPaths {
		if p == relPath {
			return true
		}
	}
	return false
}

// pinnedSectionCSS is the CSS for the pinned section, shared across pages.
const pinnedSectionCSS = `.pinned-card{margin-bottom:12px}` +
	`.pinned-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(200px,1fr));gap:10px;padding:14px}` +
	`.pin-item{display:flex;align-items:center;gap:10px;padding:10px 12px;border:1px solid var(--border);border-radius:8px;background:var(--bg);transition:background .15s,border-color .15s;position:relative}` +
	`.pin-item:hover{border-color:var(--blue);background:var(--active)}` +
	`.pin-item-icon{font-size:20px;flex-shrink:0}` +
	`.pin-item-info{flex:1;min-width:0}` +
	`.pin-item-name{font-weight:500;font-size:14px;color:var(--text);white-space:nowrap;overflow:hidden;text-overflow:ellipsis;display:block}` +
	`.pin-item-name:hover{color:var(--blue);text-decoration:none}` +
	`.pin-item-path{font-size:11px;color:var(--muted);white-space:nowrap;overflow:hidden;text-overflow:ellipsis;margin-top:2px}` +
	`.pin-item-unpin{flex-shrink:0;background:none;border:none;cursor:pointer;font-size:13px;color:var(--muted);padding:4px 6px;border-radius:4px;transition:color .15s,background .15s;line-height:1}` +
	`.pin-item-unpin:hover{color:#d93025;background:#fce8e6}` +
	`@media(max-width:640px){` +
	`.pinned-grid{display:flex;flex-wrap:nowrap;overflow-x:auto;-webkit-overflow-scrolling:touch;scroll-snap-type:x mandatory;scrollbar-width:none;gap:10px;padding:14px}` +
	`.pinned-grid::-webkit-scrollbar{display:none}` +
	`.pin-item{flex-shrink:0;width:160px;scroll-snap-align:start}` +
	`}`

// renderPinnedSection returns the HTML for the pinned section card, or "" if there are no valid pins.
func renderPinnedSection() string {
	pinMu.RLock()
	pins := make([]string, len(pinnedPaths))
	copy(pins, pinnedPaths)
	pinMu.RUnlock()

	type pinnedEntry struct {
		relPath string
		info    os.FileInfo
	}
	var pinEntries []pinnedEntry
	for _, pin := range pins {
		absPin := filepath.Join(cfgDirectory, filepath.FromSlash(pin))
		if !isUnder(absPin, cfgDirectory) {
			continue
		}

		info, err := os.Lstat(absPin)
		if err != nil {
			continue
		}

		if info.Mode()&os.ModeSymlink != 0 {
			resolvedPin, err := filepath.EvalSymlinks(absPin)
			if err != nil || !isUnder(resolvedPin, cfgDirectory) {
				continue
			}
			info, err = os.Stat(resolvedPin)
			if err != nil {
				continue
			}
		}

		pinEntries = append(pinEntries, pinnedEntry{pin, info})
	}
	if len(pinEntries) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(`<div class="card pinned-card">`)
	sb.WriteString(`<div class="card-header"><span class="card-header-title">📌 已固定</span><span class="file-count">` + fmt.Sprintf("%d 项", len(pinEntries)) + `</span></div>`)
	sb.WriteString(`<div class="pinned-grid">`)
	for _, pe := range pinEntries {
		pinName := path.Base(pe.relPath)
		pinHref := "/" + urlEncodePath(pe.relPath)
		pinIcon := fileIcon(pinName)
		if pe.info.IsDir() {
			pinIcon = "📁"
		}
		parentPath := path.Dir(pe.relPath)
		if parentPath == "." {
			parentPath = ""
		}
		target := ""
		if !pe.info.IsDir() && (strings.HasSuffix(strings.ToLower(pinName), ".md") || isPreviewableFile(pinName)) {
			target = ` target="_blank" rel="noopener noreferrer"`
		}
		sb.WriteString(`<div class="pin-item">`)
		sb.WriteString(`<span class="pin-item-icon">` + pinIcon + `</span>`)
		sb.WriteString(`<div class="pin-item-info">`)
		sb.WriteString(`<a href="` + pinHref + `" class="pin-item-name"` + target + `>` + html.EscapeString(pinName) + `</a>`)
		if parentPath != "" {
			sb.WriteString(`<div class="pin-item-path">` + html.EscapeString("/"+parentPath) + `</div>`)
		}
		sb.WriteString(`</div>`)
		sb.WriteString(`<form method="POST" action="/pin" style="display:contents">`)
		sb.WriteString(`<input type="hidden" name="action" value="unpin">`)
		sb.WriteString(`<input type="hidden" name="path" value="` + html.EscapeString(pe.relPath) + `">`)
		sb.WriteString(`<button type="submit" class="pin-item-unpin" title="取消固定" aria-label="取消固定">✕</button>`)
		sb.WriteString(`</form>`)
		sb.WriteString(`</div>`)
	}
	sb.WriteString(`</div></div>`)
	return sb.String()
}

func addPin(relPath string) {
	pinMu.Lock()
	for _, p := range pinnedPaths {
		if p == relPath {
			pinMu.Unlock()
			return // already pinned
		}
	}
	pinnedPaths = append(pinnedPaths, relPath)
	pinMu.Unlock()
	savePins()
}

func removePin(relPath string) {
	pinMu.Lock()
	for i, p := range pinnedPaths {
		if p == relPath {
			pinnedPaths = append(pinnedPaths[:i], pinnedPaths[i+1:]...)
			break
		}
	}
	pinMu.Unlock()
	savePins()
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

	// Load persisted pins.
	loadPins()

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
	mux.HandleFunc("/pin", pinHandler)
	mux.HandleFunc("/search", searchHandler)
	mux.HandleFunc("/", handler)
	addr := ":" + cfgPort
	log.Printf("🚀 WorkSpace Portal running at http://localhost%s  dir=%s", addr, cfgDirectory)
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

	// Favicon — served without auth so browsers can display it on the login page too
	if r.URL.Path == "/favicon.svg" {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		fmt.Fprint(w, faviconSVG)
		return
	}

	if r.URL.Path == "/apple-touch-icon.png" {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write(touchIconBytes) //nolint:errcheck
		return
	}

	if r.URL.Path == "/icon-192.png" {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write(icon192Bytes) //nolint:errcheck
		return
	}

	if r.URL.Path == "/icon-512.png" {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write(icon512Bytes) //nolint:errcheck
		return
	}

	// PWA manifest and service worker must be served without auth so that
	// browsers can verify the PWA is installable before the user logs in.
	if r.URL.Path == "/manifest.json" {
		w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		fmt.Fprint(w, manifestJSON)
		return
	}

	if r.URL.Path == "/sw.js" {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		fmt.Fprint(w, serviceWorkerJS)
		return
	}

	// All other endpoints require authentication
	if !validateSession(r) {
		next := r.URL.RequestURI()
		http.Redirect(w, r, "/login?next="+url.QueryEscape(next), http.StatusSeeOther)
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

	// Handle markdown save — POST ?edit writes content back to disk atomically.
	if r.Method == http.MethodPost {
		if _, ok := query["edit"]; ok {
			if !info.IsDir() && strings.HasSuffix(strings.ToLower(absPath), ".md") {
				saveMarkdown(w, r, absPath, relPath)
				return
			}
			http.Error(w, "Edit not supported for this file type", http.StatusBadRequest)
			return
		}
	}

	// Handle file deletion — POST only to avoid accidental deletions via GET
	if r.Method == http.MethodPost {
		if _, ok := query["delete"]; ok {
			if info.IsDir() {
				http.Error(w, "Cannot delete directories", http.StatusBadRequest)
				return
			}
			if err := os.Remove(absPath); err != nil {
				log.Printf("delete %q: %v", absPath, err)
				http.Error(w, "Cannot delete file", http.StatusInternalServerError)
				return
			}
			// Redirect to the parent directory after deletion
			parent := path.Dir(relPath)
			if parent == "." {
				parent = ""
			}
			var redirectURL string
			if parent == "" {
				redirectURL = "/"
			} else {
				redirectURL = "/" + urlEncodePath(parent)
			}
			http.Redirect(w, r, redirectURL, http.StatusSeeOther)
			return
		}
	}

	if info.IsDir() {
		listDirectory(w, r, absPath, relPath)
	} else if strings.HasSuffix(strings.ToLower(absPath), ".md") {
		if _, ok := query["raw"]; ok {
			serveFile(w, r, absPath)
		} else {
			renderMarkdown(w, absPath, relPath)
		}
	} else if _, ok := query["raw"]; ok {
		serveFile(w, r, absPath)
	} else if isPDFFile(absPath) {
		previewPDF(w, absPath, relPath)
	} else if isImageFile(absPath) {
		previewImage(w, absPath, relPath)
	} else if isTextFile(absPath) {
		previewText(w, absPath, relPath)
	} else {
		serveFile(w, r, absPath)
	}
}

// ─── Search ───────────────────────────────────────────────────────────────────

type searchResult struct {
	RelPath string
	Name    string
	IsDir   bool
	Size    int64
	Mtime   time.Time
}

func searchHandler(w http.ResponseWriter, r *http.Request) {
	if !validateSession(r) {
		next := r.URL.RequestURI()
		http.Redirect(w, r, "/login?next="+url.QueryEscape(next), http.StatusSeeOther)
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))

	var results []searchResult
	if query != "" {
		lowerQ := strings.ToLower(query)
		ctx := r.Context()
		walkErr := filepath.WalkDir(cfgDirectory, func(p string, d os.DirEntry, err error) error {
			// Stop if the client disconnected.
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				return nil // skip unreadable entries
			}
			// Skip the root itself.
			if p == cfgDirectory {
				return nil
			}
			// Skip symlinks to avoid escaping cfgDirectory via symlink targets.
			if d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if strings.Contains(strings.ToLower(d.Name()), lowerQ) {
				rel, relErr := filepath.Rel(cfgDirectory, p)
				if relErr != nil {
					return nil
				}
				rel = filepath.ToSlash(rel)
				info, infoErr := d.Info()
				if infoErr != nil {
					return nil
				}
				results = append(results, searchResult{
					RelPath: rel,
					Name:    d.Name(),
					IsDir:   d.IsDir(),
					Size:    info.Size(),
					Mtime:   info.ModTime(),
				})
				if len(results) >= searchMaxResults {
					return fs.SkipAll // reached cap; stop walking
				}
			}
			return nil
		})
		// Only log unexpected walk errors; ignore context cancellation, deadline, and SkipAll.
		if walkErr != nil && !errors.Is(walkErr, context.Canceled) && !errors.Is(walkErr, context.DeadlineExceeded) && !errors.Is(walkErr, fs.SkipAll) {
			log.Printf("search walk error: %v", walkErr)
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE html><html lang="zh-CN"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="theme-color" content="#1a73e8">
<link rel="manifest" href="/manifest.json">
<link rel="icon" href="/favicon.svg" type="image/svg+xml">
<link rel="apple-touch-icon" sizes="180x180" href="/apple-touch-icon.png">
<title>🔍 搜索 — WorkSpace</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
:root{--blue:#1a73e8;--blue-dark:#1558b0;--bg:#f0f2f5;--card:#fff;--border:#e8eaed;--text:#202124;--muted:#5f6368;--hover:#f8f9fa;--active:#e8f0fe}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Oxygen,Ubuntu,sans-serif;background:var(--bg);color:var(--text);min-height:100vh;-webkit-tap-highlight-color:transparent}
a{color:var(--blue);text-decoration:none}
a:hover{text-decoration:underline}
.header{background:linear-gradient(135deg,#1a73e8 0%,#1558b0 50%,#0d47a1 100%);color:#fff;padding:0 16px;height:56px;display:flex;align-items:center;justify-content:space-between;position:sticky;top:0;z-index:100;box-shadow:0 2px 12px rgba(0,0,0,.18)}
.header-left{display:flex;align-items:center;gap:8px;flex-shrink:0}
.header-logo{font-size:20px;flex-shrink:0}
.header-brand{font-size:15px;font-weight:700;letter-spacing:.5px;white-space:nowrap}
.header-right{display:flex;align-items:center;gap:8px;flex:1;justify-content:flex-end;min-width:0}
.btn-logout{color:#fff;background:rgba(255,255,255,.15);border:1px solid rgba(255,255,255,.25);padding:6px 14px;border-radius:20px;font-size:13px;cursor:pointer;transition:background .2s,transform .2s;white-space:nowrap;flex-shrink:0;-webkit-tap-highlight-color:transparent;touch-action:manipulation}
.btn-logout:hover{background:rgba(255,255,255,.25);text-decoration:none}
.btn-logout:active{background:rgba(255,255,255,.35);transform:scale(.96)}
.search-bar{display:flex;align-items:center;gap:8px;background:rgba(255,255,255,.15);border:1px solid rgba(255,255,255,.25);border-radius:24px;padding:6px 14px;max-width:480px;flex:1;min-width:0;transition:background .2s,border-color .2s}
.search-bar:focus-within{background:rgba(255,255,255,.25);border-color:rgba(255,255,255,.5)}
.search-bar input{background:none;border:none;outline:none;color:#fff;font-size:14px;width:100%;min-width:0}
.search-bar input::placeholder{color:rgba(255,255,255,.6)}
.search-bar button{background:none;border:none;color:#fff;cursor:pointer;font-size:14px;padding:2px;line-height:1;flex-shrink:0;touch-action:manipulation}
.container{max-width:1200px;margin:12px auto;padding:0 12px 24px}
.card{background:var(--card);border-radius:12px;box-shadow:0 1px 6px rgba(0,0,0,.08);overflow:hidden}
.card-header{padding:14px 16px;border-bottom:1px solid var(--border);display:flex;align-items:center;justify-content:space-between;flex-wrap:wrap;gap:8px}
.card-header-title{font-size:14px;font-weight:600;color:var(--muted)}
.file-count{font-size:12px;color:var(--muted);background:#f1f3f4;padding:3px 10px;border-radius:12px;font-weight:500}
.file-table{width:100%;border-collapse:collapse}
.file-table th{background:#f8f9fa;padding:10px 16px;text-align:left;font-size:12px;font-weight:600;color:var(--muted);letter-spacing:.5px;text-transform:uppercase;white-space:nowrap}
.file-table td{padding:10px 16px;border-top:1px solid var(--border);font-size:14px;vertical-align:middle;transition:background .15s ease}
.file-table tbody tr:hover td{background:var(--hover)}
.col-name{min-width:160px}
.col-path{color:var(--muted);font-size:13px}
.col-size{width:90px;text-align:right;color:var(--muted)}
.col-mtime{width:130px;color:var(--muted)}
.col-action{width:60px;text-align:center}
.file-name{display:flex;align-items:center;gap:8px}
.file-icon{font-size:18px;flex-shrink:0;line-height:1}
.file-link{font-weight:500;color:var(--text);transition:color .15s}
.file-link:hover{color:var(--blue);text-decoration:none}
.dir-link{color:var(--blue) !important}
.btn-dl{display:inline-flex;align-items:center;gap:3px;padding:5px 12px;border:1px solid var(--border);border-radius:8px;color:var(--muted);font-size:12px;transition:border-color .2s,color .2s,background .2s,transform .2s;background:var(--card);touch-action:manipulation}
.btn-dl:hover{border-color:var(--blue);color:var(--blue);background:#e8f0fe;text-decoration:none}
.btn-dl:active{transform:scale(.95)}
.empty{text-align:center;padding:48px 24px;color:var(--muted)}
.empty-icon{font-size:48px;margin-bottom:12px}
.empty-text{font-size:15px}
.hint{text-align:center;padding:48px 24px;color:var(--muted)}
.hint-icon{font-size:48px;margin-bottom:12px}
.hint-text{font-size:15px}
/* Pinned section */
` + pinnedSectionCSS + `
@media(max-width:640px){
  .header{height:auto;min-height:56px;flex-wrap:wrap;padding:10px 12px;gap:8px}
  .header-left{width:auto}
  .header-brand{display:none}
  .header-right{width:100%;gap:8px}
  .search-bar{max-width:none;flex:1;padding:8px 14px}
  .search-bar input{font-size:15px}
  .btn-logout{padding:8px 14px;font-size:13px}
  .container{padding:0 8px 24px}
  .file-table thead{display:none}
  .file-table,.file-table tbody,.file-table tr{display:block;width:100%}
  .file-table tr{padding:12px;border-top:1px solid var(--border);display:flex;flex-wrap:wrap;gap:4px 10px;align-items:center;min-height:48px;transition:background .15s}
  .file-table tr:active{background:var(--active)}
  .file-table td{padding:0;border:none;font-size:14px;display:inline-flex;align-items:center}
  .col-name{width:100%;order:1;display:flex}
  .col-name .file-name{gap:10px}
  .col-name .file-icon{font-size:20px}
  .col-name .file-link{font-size:15px}
  .col-path{width:100%;order:2;font-size:12px}
  .col-size{order:3;color:var(--muted);font-size:12px}
  .col-mtime{order:4;font-size:12px;flex:1;text-align:right;justify-content:flex-end}
  .col-action{order:5;margin-left:4px}
  .btn-dl{padding:6px 10px;font-size:13px}
}
</style>
</head>
<body>
<header class="header">
  <div class="header-left">
    <span class="header-logo">🚀</span>
    <span class="header-brand">WorkSpace</span>
  </div>
  <div class="header-right">
    <form class="search-bar" action="/search" method="GET">
      <span>🔍</span>
      <input type="text" name="q" placeholder="搜索文件名…" value="` + html.EscapeString(query) + `" autofocus>
      <button type="submit">搜索</button>
    </form>
    <a href="/" class="btn-logout">🏠 根目录</a>
    <a href="/logout" class="btn-logout">退出登录</a>
  </div>
</header>
`)

	sb.WriteString(`<div class="container">`)
	sb.WriteString(renderPinnedSection())
	sb.WriteString(`<div class="card">`)

	if query == "" {
		sb.WriteString(`<div class="hint"><div class="hint-icon">🔍</div><div class="hint-text">请在上方输入关键词搜索文件</div></div>`)
	} else if len(results) == 0 {
		sb.WriteString(`<div class="card-header"><span class="card-header-title">🔍 搜索：` + html.EscapeString(query) + `</span><span class="file-count">0 项</span></div>`)
		sb.WriteString(`<div class="empty"><div class="empty-icon">🔎</div><div class="empty-text">未找到匹配的文件或目录</div></div>`)
	} else {
		sb.WriteString(`<div class="card-header"><span class="card-header-title">🔍 搜索：` + html.EscapeString(query) + `</span><span class="file-count">` + fmt.Sprintf("%d 项", len(results)) + `</span></div>`)
		sb.WriteString(`<table class="file-table"><thead><tr>`)
		sb.WriteString(`<th class="col-name">名称</th>`)
		sb.WriteString(`<th class="col-path">路径</th>`)
		sb.WriteString(`<th class="col-size">大小</th>`)
		sb.WriteString(`<th class="col-mtime">修改时间</th>`)
		sb.WriteString(`<th class="col-action">操作</th>`)
		sb.WriteString(`</tr></thead><tbody>`)

		for _, res := range results {
			escapedName := html.EscapeString(res.Name)
			hrefPath := "/" + urlEncodePath(res.RelPath)
			parentPath := path.Dir(res.RelPath)
			if parentPath == "." {
				parentPath = ""
			}
			var parentHref string
			if parentPath == "" {
				parentHref = "/"
			} else {
				parentHref = "/" + urlEncodePath(parentPath)
			}

			var icon, sizeStr, actionBtn string
			if res.IsDir {
				icon = "📁"
				sizeStr = "—"
				actionBtn = ""
			} else {
				icon = fileIcon(res.Name)
				sizeStr = humanSize(res.Size)
				actionBtn = `<a href="` + hrefPath + `?download=1" class="btn-dl" title="下载">⬇</a>`
			}

			mtimeStr := formatMtime(res.Mtime)
			linkClass := "file-link"
			if res.IsDir {
				linkClass += " dir-link"
			}
			target := ""
			if strings.HasSuffix(strings.ToLower(res.Name), ".md") || isPreviewableFile(res.Name) {
				target = ` target="_blank" rel="noopener noreferrer"`
			}

			// Show parent directory as the path column
			var pathDisplay string
			if parentPath == "" {
				pathDisplay = "/"
			} else {
				pathDisplay = "/" + html.EscapeString(parentPath)
			}

			sb.WriteString(`<tr>`)
			sb.WriteString(`<td class="col-name"><div class="file-name"><span class="file-icon">` + icon + `</span><a href="` + hrefPath + `" class="` + linkClass + `"` + target + `>` + escapedName + `</a></div></td>`)
			sb.WriteString(`<td class="col-path"><a href="` + parentHref + `" class="file-link" style="color:var(--muted);font-size:12px">` + pathDisplay + `</a></td>`)
			sb.WriteString(`<td class="col-size">` + sizeStr + `</td>`)
			sb.WriteString(`<td class="col-mtime">` + mtimeStr + `</td>`)
			sb.WriteString(`<td class="col-action">` + actionBtn + `</td>`)
			sb.WriteString(`</tr>`)
		}
		sb.WriteString(`</tbody></table>`)
	}

	sb.WriteString(`</div></div><script>if('serviceWorker' in navigator){navigator.serviceWorker.register('/sw.js').catch(function(){});}</script></body></html>`)
	fmt.Fprint(w, sb.String())
}

// ─── Pin handler ──────────────────────────────────────────────────────────────

func pinHandler(w http.ResponseWriter, r *http.Request) {
	if !validateSession(r) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
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

	action := r.FormValue("action") // "pin" or "unpin"
	relPath := r.FormValue("path")

	// Sanitise relPath.
	relPath = path.Clean(relPath)
	if relPath == "." {
		relPath = ""
	}
	if relPath == "" || strings.HasPrefix(relPath, "../") || relPath == ".." {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}

	absPath := filepath.Join(cfgDirectory, filepath.FromSlash(relPath))
	if !isUnder(absPath, cfgDirectory) {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}
	if action == "pin" {
		if _, err := os.Stat(absPath); err != nil {
			http.Error(w, "File not found", http.StatusNotFound)
			return
		}
	}

	switch action {
	case "pin":
		addPin(relPath)
	case "unpin":
		removePin(relPath)
	default:
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// Compute the redirect target from the validated relPath to avoid relying on
	// a user-controlled "next" value.
	// After pin: go to the parent directory of the pinned item (i.e. where we came from).
	// After unpin: go to "/" so the user sees the updated pinned section on the homepage.
	var redirectURL string
	if action == "unpin" {
		redirectURL = "/"
	} else {
		parent := path.Dir(relPath)
		if parent == "." {
			parent = ""
		}
		if parent == "" {
			redirectURL = "/"
		} else {
			redirectURL = "/" + urlEncodePath(parent)
		}
	}
	// Validate the constructed URL is a safe relative path before redirecting.
	if pu, err := url.Parse(redirectURL); err != nil || pu.Scheme != "" || pu.Host != "" {
		redirectURL = "/"
	}
	http.Redirect(w, r, redirectURL, http.StatusSeeOther)
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
<meta name="theme-color" content="#1a73e8">
<link rel="manifest" href="/manifest.json">
<link rel="icon" href="/favicon.svg" type="image/svg+xml">
<link rel="apple-touch-icon" sizes="180x180" href="/apple-touch-icon.png">
<title>🚀 WorkSpace — /` + html.EscapeString(relPath) + `</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
:root{--blue:#1a73e8;--blue-dark:#1558b0;--bg:#f0f2f5;--card:#fff;--border:#e8eaed;--text:#202124;--muted:#5f6368;--hover:#f8f9fa;--active:#e8f0fe}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Oxygen,Ubuntu,sans-serif;background:var(--bg);color:var(--text);min-height:100vh;-webkit-tap-highlight-color:transparent}
a{color:var(--blue);text-decoration:none}
a:hover{text-decoration:underline}

/* Header */
.header{background:linear-gradient(135deg,#1a73e8 0%,#1558b0 50%,#0d47a1 100%);color:#fff;padding:0 16px;height:56px;display:flex;align-items:center;justify-content:space-between;position:sticky;top:0;z-index:100;box-shadow:0 2px 12px rgba(0,0,0,.18)}
.header-left{display:flex;align-items:center;gap:8px;flex-shrink:0}
.header-logo{font-size:20px;flex-shrink:0}
.header-brand{font-size:15px;font-weight:700;letter-spacing:.5px;white-space:nowrap}
.header-title{font-size:13px;opacity:.85;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;max-width:200px}
.header-right{display:flex;align-items:center;gap:8px;flex:1;justify-content:flex-end;min-width:0}
.btn-logout{color:#fff;background:rgba(255,255,255,.15);border:1px solid rgba(255,255,255,.25);padding:6px 14px;border-radius:20px;font-size:13px;cursor:pointer;transition:background .2s,transform .2s;white-space:nowrap;flex-shrink:0;-webkit-tap-highlight-color:transparent;touch-action:manipulation}
.btn-logout:hover{background:rgba(255,255,255,.25);text-decoration:none}
.btn-logout:active{background:rgba(255,255,255,.35);transform:scale(.96)}
.search-bar{display:flex;align-items:center;gap:8px;background:rgba(255,255,255,.15);border:1px solid rgba(255,255,255,.25);border-radius:24px;padding:6px 14px;max-width:320px;flex:1;min-width:0;transition:background .2s,border-color .2s}
.search-bar:focus-within{background:rgba(255,255,255,.25);border-color:rgba(255,255,255,.5)}
.search-bar input{background:none;border:none;outline:none;color:#fff;font-size:14px;width:100%;min-width:0}
.search-bar input::placeholder{color:rgba(255,255,255,.6)}
.search-bar button{background:none;border:none;color:#fff;cursor:pointer;font-size:14px;padding:2px;line-height:1;flex-shrink:0;touch-action:manipulation}

/* Breadcrumb */
.breadcrumb{padding:12px 16px 0;display:flex;flex-wrap:wrap;gap:4px;align-items:center;font-size:13px;color:var(--muted)}
.breadcrumb a{color:var(--blue);padding:2px 4px;border-radius:4px;transition:background .15s}
.breadcrumb a:hover{background:var(--active);text-decoration:none}
.breadcrumb span{color:var(--muted)}

/* Container */
.container{max-width:1200px;margin:12px auto;padding:0 12px 24px}

/* File table card */
.card{background:var(--card);border-radius:12px;box-shadow:0 1px 6px rgba(0,0,0,.08);overflow:hidden}
.card-header{padding:14px 16px;border-bottom:1px solid var(--border);display:flex;align-items:center;justify-content:space-between;flex-wrap:wrap;gap:8px}
.card-header-title{font-size:14px;font-weight:600;color:var(--muted)}
.file-count{font-size:12px;color:var(--muted);background:#f1f3f4;padding:3px 10px;border-radius:12px;font-weight:500}

/* Mobile sort bar — hidden on desktop */
.mobile-sort-bar{display:none}

/* Table */
.file-table{width:100%;border-collapse:collapse}
.file-table th{background:#f8f9fa;padding:10px 16px;text-align:left;font-size:12px;font-weight:600;color:var(--muted);letter-spacing:.5px;text-transform:uppercase;white-space:nowrap;position:sticky;top:56px;z-index:10}
.file-table th a{color:var(--muted);display:inline-flex;align-items:center;gap:2px}
.file-table th a:hover{color:var(--blue);text-decoration:none}
.file-table td{padding:10px 16px;border-top:1px solid var(--border);font-size:14px;vertical-align:middle;transition:background .15s ease}
.file-table tbody tr:hover td{background:var(--hover)}
.file-table .col-name{min-width:160px}
.file-table .col-size{width:90px;text-align:right;color:var(--muted)}
.file-table .col-mtime{width:130px;color:var(--muted)}
.file-table .col-action{width:150px;text-align:center}
.file-name{display:flex;align-items:center;gap:8px}
.file-icon{font-size:18px;flex-shrink:0;line-height:1}
.file-link{font-weight:500;color:var(--text);transition:color .15s}
.file-link:hover{color:var(--blue);text-decoration:none}
.dir-link{color:var(--blue) !important}
.btn-dl{display:inline-flex;align-items:center;gap:3px;padding:5px 12px;border:1px solid var(--border);border-radius:8px;color:var(--muted);font-size:12px;transition:border-color .2s,color .2s,background .2s,transform .2s;background:var(--card);touch-action:manipulation}
.btn-dl:hover{border-color:var(--blue);color:var(--blue);background:#e8f0fe;text-decoration:none}
.btn-dl:active{transform:scale(.95)}
.btn-del{display:inline-flex;align-items:center;padding:5px 10px;border:1px solid var(--border);border-radius:8px;color:var(--muted);font-size:12px;transition:border-color .2s,color .2s,background .2s,transform .2s;background:var(--card);cursor:pointer;line-height:1;touch-action:manipulation}
.btn-del:hover{border-color:#d93025;color:#d93025;background:#fce8e6}
.btn-del:active{transform:scale(.95)}
.btn-pin{display:inline-flex;align-items:center;padding:5px 10px;border:1px solid var(--border);border-radius:8px;color:var(--muted);font-size:12px;transition:border-color .2s,color .2s,background .2s,transform .2s;background:var(--card);cursor:pointer;line-height:1;touch-action:manipulation}
.btn-pin:hover{border-color:#f9a825;color:#f9a825;background:#fff8e1}
.btn-pin:active{transform:scale(.95)}
.btn-pin--pinned{border-color:#f9a825;color:#f9a825;background:#fff8e1}
.btn-pin--pinned:hover{border-color:#d93025;color:#d93025;background:#fce8e6}
.action-btns{display:flex;gap:6px;justify-content:center;align-items:center}

/* Pinned section */
` + pinnedSectionCSS + `

/* Mobile cards view */
@media(max-width:640px){
  .header{height:auto;min-height:56px;flex-wrap:wrap;padding:10px 12px;gap:8px}
  .header-left{width:auto}
  .header-brand{display:none}
  .header-title{max-width:140px}
  .header-right{width:100%;gap:8px}
  .search-bar{max-width:none;flex:1;order:0;padding:8px 14px}
  .search-bar input{font-size:15px}
  .btn-logout{padding:8px 14px;font-size:13px}
  .container{padding:0 8px 24px}
  .breadcrumb{padding:10px 12px 0;font-size:14px;gap:4px}

  /* Mobile sort bar */
  .mobile-sort-bar{display:flex;align-items:center;gap:6px;padding:10px 12px;border-bottom:1px solid var(--border);font-size:13px;color:var(--muted);flex-wrap:wrap;background:#fafafa}
  .sort-chip{padding:6px 14px;border:1px solid var(--border);border-radius:16px;color:var(--muted);font-size:13px;background:var(--card);white-space:nowrap;touch-action:manipulation;transition:border-color .15s,color .15s,background .15s,transform .15s}
  .sort-chip--active{border-color:var(--blue);color:var(--blue);background:#e8f0fe;font-weight:600}
  .sort-chip:hover{text-decoration:none;border-color:var(--blue);color:var(--blue)}
  .sort-chip:active{transform:scale(.96)}

  /* Hide thead, render rows as single-line items */
  .file-table thead{display:none}
  .file-table,.file-table tbody,.file-table tr{display:block;width:100%}
  .file-table tr{padding:0 12px;border-top:1px solid var(--border);display:flex;flex-wrap:nowrap;gap:0 8px;align-items:center;min-height:52px;transition:background .15s}
  .file-table tr:active{background:var(--active)}
  .file-table td{padding:0;border:none;font-size:14px;display:inline-flex;align-items:center;flex-shrink:0}
  .file-table .col-name{flex:1;min-width:0;display:flex;overflow:hidden}
  .file-table .col-name .file-name{gap:8px;min-width:0;flex:1;overflow:hidden}
  .file-table .col-name .file-icon{font-size:20px;flex-shrink:0}
  .file-table .col-name .file-link{font-size:14px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;min-width:0}
  .file-table .col-size{display:none}
  .file-table .col-mtime{display:none}
  .file-table .col-action{flex-shrink:0}
  .btn-dl,.btn-del,.btn-pin{padding:6px 8px;font-size:13px}
}

/* Empty state */
.empty{text-align:center;padding:48px 24px;color:var(--muted)}
.empty-icon{font-size:48px;margin-bottom:12px}
.empty-text{font-size:15px}
</style>
</head>
<body>
<header class="header">
  <div class="header-left">
    <span class="header-logo">🚀</span>
    <span class="header-brand">WorkSpace</span>
    <span class="header-title">` + html.EscapeString("/"+relPath) + `</span>
  </div>
  <div class="header-right">
    <form class="search-bar" action="/search" method="GET">
      <span>🔍</span>
      <input type="text" name="q" placeholder="搜索文件名…">
      <button type="submit">搜</button>
    </form>
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

	sb.WriteString(`<div class="container">`)

	// Pinned section — shown on all directory pages.
	sb.WriteString(renderPinnedSection())

	sb.WriteString(`<div class="card">`)
	sb.WriteString(`<div class="card-header"><span class="card-header-title">📂 文件列表</span><span class="file-count">` + fmt.Sprintf("%d 项", len(files)) + `</span></div>`)

	// Mobile sort bar — visible only on small screens since the table header is hidden there
	mobileSortBar := `<div class="mobile-sort-bar"><span>排序:</span>`
	for _, f := range []struct{ field, label string }{{"name", "名称"}, {"size", "大小"}, {"mtime", "修改时间"}} {
		cls := "sort-chip"
		if sortBy == f.field {
			cls += " sort-chip--active"
		}
		mobileSortBar += `<a href="` + sortLink(f.field) + `" class="` + cls + `">` + f.label + sortIndicator(f.field) + `</a>`
	}
	mobileSortBar += `</div>`
	sb.WriteString(mobileSortBar)

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

			// Compute the pin path (slash-separated relative path from cfgDirectory).
			var filePinPath string
			if relPath == "" {
				filePinPath = f.Name
			} else {
				filePinPath = relPath + "/" + f.Name
			}
			pinned := isPinned(filePinPath)
			var pinAction, pinClass string
			if pinned {
				pinAction = "unpin"
				pinClass = "btn-pin btn-pin--pinned"
			} else {
				pinAction = "pin"
				pinClass = "btn-pin"
			}
			pinTitle := "固定到首页"
			if pinned {
				pinTitle = "取消固定"
			}
			pinBtn := `<form method="POST" action="/pin" style="display:contents">` +
				`<input type="hidden" name="action" value="` + pinAction + `">` +
				`<input type="hidden" name="path" value="` + html.EscapeString(filePinPath) + `">` +
				`<button type="submit" class="` + pinClass + `" title="` + pinTitle + `">📌</button>` +
				`</form>`

			var icon, sizeStr, actionBtn string
			if f.IsDir {
				icon = "📁"
				sizeStr = "—"
				actionBtn = `<div class="action-btns">` + pinBtn + `</div>`
			} else {
				icon = fileIcon(f.Name)
				sizeStr = humanSize(f.Size)
				actionBtn = `<div class="action-btns">` +
					pinBtn +
					`<a href="` + hrefPath + `?download=1" class="btn-dl" title="下载">⬇</a>` +
					`<form method="POST" action="` + hrefPath + `?delete=1" style="display:contents">` +
					`<button type="submit" class="btn-del" title="删除" aria-label="删除" data-name="` + escapedName + `" onclick='return confirm("确定要删除文件 \""+this.dataset.name+"\" 吗？此操作不可撤销！")'>🗑</button>` +
					`</form></div>`
			}

			mtimeStr := formatMtime(f.Mtime)
			linkClass := "file-link"
			if f.IsDir {
				linkClass += " dir-link"
			}
			target := ""
			if strings.HasSuffix(strings.ToLower(f.Name), ".md") || isPreviewableFile(f.Name) {
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

	sb.WriteString(`</div></div><script>if('serviceWorker' in navigator){navigator.serviceWorker.register('/sw.js').catch(function(){});}</script></body></html>`)
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

	dlURL := "/" + urlEncodePath(relPath) + "?download=1"

	fileName := filepath.Base(relPath)
	escapedSource := html.EscapeString(string(content))

	// Determine current pin state for this file.
	pinned := isPinned(relPath)
	var pinBtnClass, pinBtnAction, pinBtnTitle string
	if pinned {
		pinBtnClass = "btn-pin-md btn-pin-md--pinned"
		pinBtnAction = "unpin"
		pinBtnTitle = "取消固定"
	} else {
		pinBtnClass = "btn-pin-md"
		pinBtnAction = "pin"
		pinBtnTitle = "固定到首页"
	}
	pinForm := `<form id="pin-form" method="POST" action="/pin" style="display:contents">` +
		`<input type="hidden" name="action" value="` + pinBtnAction + `">` +
		`<input type="hidden" name="path" value="` + html.EscapeString(relPath) + `">` +
		`<button type="submit" id="btn-pin" class="` + pinBtnClass + `" title="` + pinBtnTitle + `">📌</button>` +
		`</form>`

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	fmt.Fprintf(w, `<!DOCTYPE html><html lang="zh-CN"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="theme-color" content="#1a73e8">
<link rel="manifest" href="/manifest.json">
<link rel="icon" href="/favicon.svg" type="image/svg+xml">
<link rel="apple-touch-icon" sizes="180x180" href="/apple-touch-icon.png">
<title>%s — WorkSpace</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
:root{--blue:#1a73e8;--bg:#f0f2f5;--card:#fff;--border:#e8eaed;--text:#202124;--muted:#5f6368;--code-bg:#f6f8fa}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Oxygen,Ubuntu,sans-serif;background:var(--bg);color:var(--text);min-height:100vh;-webkit-tap-highlight-color:transparent}
a{color:var(--blue);text-decoration:none}
a:hover{text-decoration:underline}

/* Header */
.header{background:linear-gradient(135deg,#1a73e8 0%%,#1558b0 50%%,#0d47a1 100%%);color:#fff;padding:0 16px;height:56px;display:flex;align-items:center;justify-content:space-between;position:sticky;top:0;z-index:100;box-shadow:0 2px 12px rgba(0,0,0,.18)}
.header-left{display:flex;align-items:center;gap:8px;flex-shrink:0}
.header-logo{font-size:20px;flex-shrink:0}
.header-brand{font-size:15px;font-weight:700;letter-spacing:.5px;white-space:nowrap}
.header-title{font-size:13px;opacity:.85;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;max-width:200px}
.header-right{display:flex;align-items:center;gap:8px;flex-shrink:0}
.btn-back{color:#fff;background:rgba(255,255,255,.15);border:1px solid rgba(255,255,255,.25);padding:6px 14px;border-radius:20px;font-size:13px;transition:background .2s,transform .2s;white-space:nowrap;-webkit-tap-highlight-color:transparent;touch-action:manipulation}
.btn-back:hover{background:rgba(255,255,255,.25);text-decoration:none}
.btn-back:active{background:rgba(255,255,255,.35);transform:scale(.96)}
.btn-logout{color:rgba(255,255,255,.8);font-size:13px;padding:6px 12px;border-radius:20px;transition:background .2s,color .2s,transform .2s;-webkit-tap-highlight-color:transparent;touch-action:manipulation}
.btn-logout:hover{background:rgba(255,255,255,.15);text-decoration:none;color:#fff}
.btn-logout:active{background:rgba(255,255,255,.25);transform:scale(.96)}
.btn-dl{color:#fff;background:rgba(255,255,255,.15);border:1px solid rgba(255,255,255,.25);padding:6px 14px;border-radius:20px;font-size:13px;transition:background .2s,transform .2s;white-space:nowrap;-webkit-tap-highlight-color:transparent;touch-action:manipulation}
.btn-dl:hover{background:rgba(255,255,255,.25);text-decoration:none}
.btn-dl:active{background:rgba(255,255,255,.35);transform:scale(.96)}
.btn-edit{color:#fff;background:rgba(255,255,255,.15);border:1px solid rgba(255,255,255,.25);padding:6px 14px;border-radius:20px;font-size:13px;cursor:pointer;transition:background .2s,transform .2s;white-space:nowrap;-webkit-tap-highlight-color:transparent;touch-action:manipulation}
.btn-edit:hover{background:rgba(255,255,255,.25)}
.btn-edit:active{background:rgba(255,255,255,.35);transform:scale(.96)}
.btn-save{color:#fff;background:#1e8a3c;border:1px solid rgba(255,255,255,.3);padding:6px 14px;border-radius:20px;font-size:13px;cursor:pointer;transition:background .2s,transform .2s;white-space:nowrap;-webkit-tap-highlight-color:transparent;touch-action:manipulation}
.btn-save:hover{background:#176b2f}
.btn-save:active{transform:scale(.96)}
.btn-cancel{color:#fff;background:rgba(255,255,255,.15);border:1px solid rgba(255,255,255,.25);padding:6px 14px;border-radius:20px;font-size:13px;cursor:pointer;transition:background .2s,transform .2s;white-space:nowrap;-webkit-tap-highlight-color:transparent;touch-action:manipulation}
.btn-cancel:hover{background:rgba(255,255,255,.25)}
.btn-cancel:active{background:rgba(255,255,255,.35);transform:scale(.96)}
.btn-pin-md{color:#fff;background:rgba(255,255,255,.15);border:1px solid rgba(255,255,255,.25);padding:6px 14px;border-radius:20px;font-size:13px;cursor:pointer;transition:background .2s,transform .2s;white-space:nowrap;-webkit-tap-highlight-color:transparent;touch-action:manipulation;line-height:1}
.btn-pin-md:hover{background:rgba(255,255,255,.25)}
.btn-pin-md:active{background:rgba(255,255,255,.35);transform:scale(.96)}
.btn-pin-md--pinned{background:rgba(249,168,37,.35);border-color:rgba(249,168,37,.6)}
.btn-pin-md--pinned:hover{background:rgba(249,168,37,.5)}

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
  .header{height:auto;min-height:48px;flex-wrap:wrap;padding:8px 12px;gap:6px}
  .header-left{width:auto}
  .header-brand{display:none}
  .header-title{max-width:120px}
  .header-right{gap:6px}
  .btn-back,.btn-dl,.btn-edit,.btn-save,.btn-cancel,.btn-pin-md{padding:8px 14px;font-size:13px}
  .btn-logout{padding:8px 12px;font-size:13px}
  .wrapper{padding:0 8px 32px;margin:12px auto}
  .md-card{padding:24px 16px;border-radius:10px}
  .md-body{font-size:15px}
  .md-body h1{font-size:1.5em}
  .md-body h2{font-size:1.25em}
  .md-editor{font-size:15px;padding:16px}
}
</style>
</head>
<body>
<header class="header">
  <div class="header-left">
    <span class="header-logo">📄</span>
    <span class="header-brand">WorkSpace</span>
    <span class="header-title">%s</span>
  </div>
  <div class="header-right">
    <a href="%s" id="btn-back" class="btn-back">← 返回</a>
    %s
    <a href="%s" id="btn-dl" class="btn-dl">⬇ 下载</a>
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
  document.getElementById('btn-dl').style.display='none';
  document.getElementById('btn-pin').style.display='none';
  document.getElementById('btn-save').style.display='';
  document.getElementById('btn-cancel').style.display='';
}
function cancelEdit(){
  document.getElementById('preview-card').style.display='';
  document.getElementById('edit-form').style.display='none';
  document.getElementById('btn-edit').style.display='';
  document.getElementById('btn-back').style.display='';
  document.getElementById('btn-dl').style.display='';
  document.getElementById('btn-pin').style.display='';
  document.getElementById('btn-save').style.display='none';
  document.getElementById('btn-cancel').style.display='none';
}
if('serviceWorker' in navigator){navigator.serviceWorker.register('/sw.js').catch(function(){});}
</script>
</body></html>`,
		html.EscapeString(fileName),
		html.EscapeString(fileName),
		parentURL,
		pinForm,
		html.EscapeString(dlURL),
		rendered.String(),
		editActionURL,
		escapedSource,
	)
}

// ─── File type detection ──────────────────────────────────────────────────────

func isPDFFile(absPath string) bool {
	return strings.ToLower(filepath.Ext(absPath)) == ".pdf"
}

func isImageFile(absPath string) bool {
	switch strings.ToLower(filepath.Ext(absPath)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".svg", ".webp", ".ico", ".bmp":
		return true
	}
	return false
}

func isTextFile(absPath string) bool {
	switch strings.ToLower(filepath.Ext(absPath)) {
	case ".txt", ".log", ".csv", ".tsv",
		".sh", ".bash", ".zsh", ".fish",
		".py", ".go", ".js", ".ts", ".jsx", ".tsx",
		".html", ".htm", ".css", ".xml",
		".json", ".yaml", ".yml", ".toml", ".ini", ".cfg", ".conf",
		".sql", ".rs", ".java", ".c", ".cpp", ".h", ".hpp",
		".rb", ".php", ".pl", ".lua", ".r",
		".env", ".gitignore", ".dockerignore",
		".rst", ".tex", ".properties":
		return true
	}
	return false
}

// isPreviewableFile returns true if the file can be previewed in the browser.
func isPreviewableFile(name string) bool {
	return isPDFFile(name) || isImageFile(name) || isTextFile(name)
}

// ─── File previews ────────────────────────────────────────────────────────────

// previewPageHeader returns the common HTML head and header bar for preview pages.
func previewPageHeader(fileName, parentURL, icon string) string {
	return fmt.Sprintf(`<!DOCTYPE html><html lang="zh-CN"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="theme-color" content="#1a73e8">
<link rel="manifest" href="/manifest.json">
<link rel="icon" href="/favicon.svg" type="image/svg+xml">
<link rel="apple-touch-icon" sizes="180x180" href="/apple-touch-icon.png">
<title>%s — WorkSpace</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
:root{--blue:#1a73e8;--bg:#f0f2f5;--card:#fff;--border:#e8eaed;--text:#202124;--muted:#5f6368;--code-bg:#f6f8fa}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Oxygen,Ubuntu,sans-serif;background:var(--bg);color:var(--text);min-height:100vh;display:flex;flex-direction:column}
a{color:var(--blue);text-decoration:none}
a:hover{text-decoration:underline}
.header{background:linear-gradient(135deg,#1a73e8,#0d47a1);color:#fff;padding:0 16px;height:56px;display:flex;align-items:center;justify-content:space-between;position:sticky;top:0;z-index:100;box-shadow:0 2px 8px rgba(0,0,0,.2);flex-shrink:0}
.header-left{display:flex;align-items:center;gap:8px;flex-shrink:0}
.header-logo{font-size:20px;flex-shrink:0}
.header-brand{font-size:15px;font-weight:700;letter-spacing:.5px;white-space:nowrap}
.header-title{font-size:13px;opacity:.85;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;max-width:200px}
.header-right{display:flex;align-items:center;gap:8px;flex-shrink:0}
.btn-back{color:#fff;background:rgba(255,255,255,.2);border:1px solid rgba(255,255,255,.3);padding:5px 12px;border-radius:20px;font-size:12px;transition:background .15s;white-space:nowrap}
.btn-back:hover{background:rgba(255,255,255,.3);text-decoration:none}
.btn-dl{color:#fff;background:rgba(255,255,255,.2);border:1px solid rgba(255,255,255,.3);padding:5px 12px;border-radius:20px;font-size:12px;transition:background .15s;white-space:nowrap}
.btn-dl:hover{background:rgba(255,255,255,.3);text-decoration:none}
.btn-logout{color:rgba(255,255,255,.8);font-size:12px;padding:5px 10px;border-radius:20px;transition:background .15s}
.btn-logout:hover{background:rgba(255,255,255,.15);text-decoration:none;color:#fff}
@media(max-width:640px){
  .header-brand{display:none}
  .header-title{max-width:120px}
}
</style>`,
		html.EscapeString(fileName),
	) + fmt.Sprintf(`
</head>
<body>
<header class="header">
  <div class="header-left">
    <span class="header-logo">%s</span>
    <span class="header-brand">WorkSpace</span>
    <span class="header-title">%s</span>
  </div>
  <div class="header-right">
    <a href="%s" class="btn-back">← 返回</a>`,
		icon,
		html.EscapeString(fileName),
		parentURL,
	)
}

func previewPDF(w http.ResponseWriter, absPath, relPath string) {
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
	fileName := filepath.Base(relPath)
	rawURL := "/" + urlEncodePath(relPath) + "?raw"
	dlURL := "/" + urlEncodePath(relPath) + "?download=1"

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	var sb strings.Builder
	sb.WriteString(previewPageHeader(fileName, parentURL, "📕"))
	sb.WriteString(`    <a href="` + dlURL + `" class="btn-dl">⬇ 下载</a>
    <a href="/logout" class="btn-logout">退出</a>
  </div>
</header>
<style>
.pdf-wrapper{flex:1;display:flex;flex-direction:column;padding:0}
.pdf-wrapper iframe{flex:1;border:none;width:100%;min-height:0}
</style>
<div class="pdf-wrapper">
  <iframe src="` + html.EscapeString(rawURL) + `" title="PDF preview: ` + html.EscapeString(fileName) + `"></iframe>
</div>
<script>if('serviceWorker' in navigator){navigator.serviceWorker.register('/sw.js').catch(function(){});}</script>
</body></html>`)
	fmt.Fprint(w, sb.String())
}

// maxTextPreviewBytes caps text file preview at 1 MiB to avoid excessive memory use.
const maxTextPreviewBytes = 1 << 20

func previewText(w http.ResponseWriter, absPath, relPath string) {
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
	fileName := filepath.Base(relPath)
	dlURL := "/" + urlEncodePath(relPath) + "?download=1"

	info, err := os.Stat(absPath)
	if err != nil {
		http.Error(w, "Cannot read file", http.StatusInternalServerError)
		return
	}
	fileSize := info.Size()

	f, err := os.Open(absPath)
	if err != nil {
		http.Error(w, "Cannot read file", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	content, err := io.ReadAll(io.LimitReader(f, maxTextPreviewBytes+1))
	if err != nil {
		http.Error(w, "Cannot read file", http.StatusInternalServerError)
		return
	}

	truncated := false
	if len(content) > maxTextPreviewBytes {
		content = content[:maxTextPreviewBytes]
		truncated = true
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	var sb strings.Builder
	sb.WriteString(previewPageHeader(fileName, parentURL, "📝"))
	sb.WriteString(`    <a href="` + dlURL + `" class="btn-dl">⬇ 下载</a>
    <a href="/logout" class="btn-logout">退出</a>
  </div>
</header>
<style>
.wrapper{max-width:1100px;margin:24px auto;padding:0 16px 48px;width:100%}
.text-card{background:var(--card);border-radius:12px;box-shadow:0 1px 4px rgba(0,0,0,.08);overflow:hidden}
.text-card-header{padding:12px 16px;border-bottom:1px solid var(--border);font-size:13px;color:var(--muted);display:flex;align-items:center;justify-content:space-between}
.text-body{padding:16px 20px;overflow-x:auto}
.text-body pre{margin:0;font-family:"SFMono-Regular",Consolas,"Liberation Mono",Menlo,monospace;font-size:13px;line-height:1.6;color:var(--text);white-space:pre-wrap;word-wrap:break-word;tab-size:4}
.text-body .line-numbers{display:inline-block;width:auto;min-width:3em;text-align:right;color:#b0b0b0;user-select:none;-webkit-user-select:none;padding-right:16px;border-right:1px solid var(--border);margin-right:16px;vertical-align:top;white-space:pre;line-height:1.6;font-family:"SFMono-Regular",Consolas,"Liberation Mono",Menlo,monospace;font-size:13px}
.text-body .code-content{display:inline-block;vertical-align:top;white-space:pre-wrap;word-wrap:break-word}
.truncated{padding:12px 16px;background:#fff8e1;color:#f57f17;font-size:13px;border-top:1px solid var(--border)}
@media(max-width:640px){
  .wrapper{padding:0 8px 32px;margin:12px auto}
  .text-body{padding:12px}
  .text-body pre{font-size:12px}
}
</style>
<div class="wrapper"><div class="text-card">
  <div class="text-card-header"><span>`)
	sb.WriteString(html.EscapeString(fileName))
	sb.WriteString(`</span><span>`)
	sb.WriteString(humanSize(fileSize))
	sb.WriteString(`</span></div>
  <div class="text-body"><pre>`)

	// Add line numbers and content
	lines := strings.Split(string(content), "\n")
	sb.WriteString(`<span class="line-numbers">`)
	for i := range lines {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(fmt.Sprintf("%d", i+1))
	}
	sb.WriteString(`</span><span class="code-content">`)
	sb.WriteString(html.EscapeString(string(content)))
	sb.WriteString(`</span>`)

	sb.WriteString(`</pre></div>`)
	if truncated {
		sb.WriteString(`<div class="truncated">⚠️ 文件过大，仅显示前 1 MiB 内容。请下载查看完整文件。</div>`)
	}
	sb.WriteString(`</div></div>
<script>if('serviceWorker' in navigator){navigator.serviceWorker.register('/sw.js').catch(function(){});}</script>
</body></html>`)
	fmt.Fprint(w, sb.String())
}

func previewImage(w http.ResponseWriter, absPath, relPath string) {
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
	fileName := filepath.Base(relPath)
	rawURL := "/" + urlEncodePath(relPath) + "?raw"
	dlURL := "/" + urlEncodePath(relPath) + "?download=1"

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	var sb strings.Builder
	sb.WriteString(previewPageHeader(fileName, parentURL, "🖼️"))
	sb.WriteString(`    <a href="` + dlURL + `" class="btn-dl">⬇ 下载</a>
    <a href="/logout" class="btn-logout">退出</a>
  </div>
</header>
<style>
.img-wrapper{flex:1;display:flex;align-items:center;justify-content:center;padding:24px;overflow:auto;background:var(--bg)}
.img-wrapper img{max-width:100%;max-height:calc(100vh - 104px);object-fit:contain;border-radius:8px;box-shadow:0 2px 12px rgba(0,0,0,.12);background:var(--card);cursor:zoom-in;transition:transform .2s}
.img-wrapper img.zoomed{max-width:none;max-height:none;cursor:zoom-out;transform:none;box-shadow:none}
.img-info{text-align:center;padding:8px;font-size:12px;color:var(--muted);background:var(--bg);flex-shrink:0}
@media(max-width:640px){
  .img-wrapper{padding:12px}
}
</style>
<div class="img-wrapper">
  <img id="preview-img" src="` + html.EscapeString(rawURL) + `" alt="` + html.EscapeString(fileName) + `" onclick="this.classList.toggle('zoomed')">
</div>
<div class="img-info" id="img-info">` + html.EscapeString(fileName) + `</div>
<script>
var img=document.getElementById('preview-img');
var info=document.getElementById('img-info');
var baseName=info.textContent;
img.onload=function(){
  info.textContent=baseName+' — '+img.naturalWidth+'×'+img.naturalHeight;
};
if('serviceWorker' in navigator){navigator.serviceWorker.register('/sw.js').catch(function(){});}
</script>
</body></html>`)
	fmt.Fprint(w, sb.String())
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

func formatMtime(t time.Time) string {
	return t.In(mtimeLocation).Format(mtimeFormat)
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
