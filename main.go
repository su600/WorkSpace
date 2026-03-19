package main

import (
	"bytes"
	"embed"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	highlighting "github.com/yuin/goldmark-highlighting/v2"

	"github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	goldmarkhtml "github.com/yuin/goldmark/renderer/html"
)

//go:embed templates
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

var rootDir string

// FileEntry represents a file or directory entry
type FileEntry struct {
	Name    string
	Path    string
	IsDir   bool
	Size    int64
	ModTime time.Time
	Ext     string
	Icon    string
}

// DirData is passed to the directory listing template
type DirData struct {
	Title       string
	CurrentPath string
	Breadcrumbs []Breadcrumb
	Dirs        []FileEntry
	Files       []FileEntry
}

// FileData is passed to the file view template
type FileData struct {
	Title       string
	CurrentPath string
	Breadcrumbs []Breadcrumb
	Content     template.HTML
	RawPath     string
	IsMarkdown  bool
	IsText      bool
	FileName    string
}

// Breadcrumb represents a navigation breadcrumb
type Breadcrumb struct {
	Name string
	Path string
}

func buildBreadcrumbs(reqPath string) []Breadcrumb {
	crumbs := []Breadcrumb{{Name: "🏠 Home", Path: "/"}}
	if reqPath == "/" || reqPath == "" {
		return crumbs
	}
	parts := strings.Split(strings.Trim(reqPath, "/"), "/")
	acc := ""
	for _, part := range parts {
		acc += "/" + part
		crumbs = append(crumbs, Breadcrumb{Name: part, Path: acc})
	}
	return crumbs
}

func sizeString(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGTPE"[exp])
}

func fileIcon(entry os.DirEntry) string {
	if entry.IsDir() {
		return "📁"
	}
	ext := strings.ToLower(filepath.Ext(entry.Name()))
	switch ext {
	case ".md", ".markdown":
		return "📝"
	case ".go":
		return "🐹"
	case ".py":
		return "🐍"
	case ".js", ".ts":
		return "📜"
	case ".html", ".htm":
		return "🌐"
	case ".css":
		return "🎨"
	case ".json", ".yaml", ".yml", ".toml":
		return "⚙️"
	case ".sh", ".bash", ".zsh":
		return "💻"
	case ".txt", ".log":
		return "📄"
	case ".pdf":
		return "📕"
	case ".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp":
		return "🖼️"
	case ".zip", ".tar", ".gz", ".bz2", ".7z":
		return "📦"
	case ".mp4", ".mkv", ".avi", ".mov":
		return "🎬"
	case ".mp3", ".wav", ".flac":
		return "🎵"
	default:
		return "📄"
	}
}

func isTextFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	textExts := map[string]bool{
		".md": true, ".markdown": true, ".txt": true, ".log": true,
		".go": true, ".py": true, ".js": true, ".ts": true, ".jsx": true, ".tsx": true,
		".html": true, ".htm": true, ".css": true, ".scss": true, ".sass": true,
		".json": true, ".yaml": true, ".yml": true, ".toml": true, ".ini": true, ".conf": true,
		".sh": true, ".bash": true, ".zsh": true, ".fish": true,
		".c": true, ".cpp": true, ".h": true, ".hpp": true,
		".java": true, ".kt": true, ".rs": true, ".rb": true, ".php": true,
		".xml": true, ".svg": true, ".env": true, ".gitignore": true,
		".dockerfile": true, ".makefile": true, ".sql": true, ".r": true,
		".lua": true, ".vim": true, ".tf": true, ".proto": true,
	}
	if textExts[ext] {
		return true
	}
	// Also check for files with no extension that are typically text
	base := strings.ToLower(filepath.Base(name))
	noExtText := map[string]bool{
		"dockerfile": true, "makefile": true, "readme": true, "license": true,
		"authors": true, "changelog": true, "contributing": true, ".gitignore": true,
		".env": true, ".editorconfig": true,
	}
	return noExtText[base]
}

func isMarkdown(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".md" || ext == ".markdown"
}

var mdParser goldmark.Markdown

func initMarkdown() {
	mdParser = goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			extension.Table,
			extension.Strikethrough,
			extension.TaskList,
			extension.DefinitionList,
			extension.Footnote,
			highlighting.NewHighlighting(
				highlighting.WithStyle("github"),
				highlighting.WithFormatOptions(
					html.WithLineNumbers(false),
					html.TabWidth(4),
				),
			),
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
		goldmark.WithRendererOptions(
			goldmarkhtml.WithHardWraps(),
			goldmarkhtml.WithXHTML(),
			goldmarkhtml.WithUnsafe(),
		),
	)
}

func renderMarkdown(src []byte) (template.HTML, error) {
	var buf bytes.Buffer
	if err := mdParser.Convert(src, &buf); err != nil {
		return "", err
	}
	return template.HTML(buf.String()), nil //nolint:gosec // markdown content is rendered from local filesystem files
}

func safePath(reqPath string) (string, error) {
	// Clean and validate path to prevent directory traversal
	clean := filepath.Clean("/" + reqPath)
	fullPath := filepath.Join(rootDir, clean)
	// Ensure the resolved path is within rootDir
	rel, err := filepath.Rel(rootDir, fullPath)
	if err != nil {
		return "", fmt.Errorf("invalid path")
	}
	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path traversal not allowed")
	}
	return fullPath, nil
}

var tmpl *template.Template

func initTemplates() {
	funcMap := template.FuncMap{
		"sizeStr": sizeString,
		"formatTime": func(t time.Time) string {
			return t.Format("2006-01-02 15:04")
		},
		"urlEncode": url.PathEscape,
		"last": func(items []Breadcrumb, i int) bool {
			return i == len(items)-1
		},
	}
	tmpl = template.Must(template.New("").Funcs(funcMap).ParseFS(templateFS, "templates/*.html"))
}

func handleDir(w http.ResponseWriter, r *http.Request, fullPath string, reqPath string) {
	entries, err := os.ReadDir(fullPath)
	if err != nil {
		http.Error(w, "Cannot read directory", http.StatusInternalServerError)
		return
	}

	var dirs, files []FileEntry
	for _, e := range entries {
		// Skip hidden files (starting with .)
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		entryPath := reqPath
		if !strings.HasSuffix(entryPath, "/") {
			entryPath += "/"
		}
		entryPath += e.Name()

		fe := FileEntry{
			Name:    e.Name(),
			Path:    entryPath,
			IsDir:   e.IsDir(),
			ModTime: info.ModTime(),
			Ext:     strings.ToLower(filepath.Ext(e.Name())),
			Icon:    fileIcon(e),
		}
		if !e.IsDir() {
			fe.Size = info.Size()
		}
		if e.IsDir() {
			dirs = append(dirs, fe)
		} else {
			files = append(files, fe)
		}
	}

	sort.Slice(dirs, func(i, j int) bool {
		return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name)
	})
	sort.Slice(files, func(i, j int) bool {
		return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name)
	})

	displayPath := reqPath
	if displayPath == "" {
		displayPath = "/"
	}

	data := DirData{
		Title:       "📁 " + filepath.Base(fullPath),
		CurrentPath: displayPath,
		Breadcrumbs: buildBreadcrumbs(reqPath),
		Dirs:        dirs,
		Files:       files,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "dir.html", data); err != nil {
		log.Printf("template error: %v", err)
	}
}

func handleFile(w http.ResponseWriter, r *http.Request, fullPath string, reqPath string) {
	fileName := filepath.Base(fullPath)

	if isMarkdown(fileName) {
		src, err := os.ReadFile(fullPath)
		if err != nil {
			http.Error(w, "Cannot read file", http.StatusInternalServerError)
			return
		}
		rendered, err := renderMarkdown(src)
		if err != nil {
			http.Error(w, "Cannot render markdown", http.StatusInternalServerError)
			return
		}
		data := FileData{
			Title:       "📝 " + fileName,
			CurrentPath: reqPath,
			Breadcrumbs: buildBreadcrumbs(reqPath),
			Content:     rendered,
			RawPath:     "/raw" + reqPath,
			IsMarkdown:  true,
			IsText:      true,
			FileName:    fileName,
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.ExecuteTemplate(w, "file.html", data); err != nil {
			log.Printf("template error: %v", err)
		}
		return
	}

	if isTextFile(fileName) {
		src, err := os.ReadFile(fullPath)
		if err != nil {
			http.Error(w, "Cannot read file", http.StatusInternalServerError)
			return
		}
		// Wrap in fenced code block for syntax highlighting
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(fileName)), ".")
		var mdSrc string
		if ext != "" {
			mdSrc = fmt.Sprintf("```%s\n%s\n```", ext, string(src))
		} else {
			mdSrc = fmt.Sprintf("```\n%s\n```", string(src))
		}
		rendered, err := renderMarkdown([]byte(mdSrc))
		if err != nil {
			// Fallback to plain text
			rendered = template.HTML("<pre>" + template.HTMLEscapeString(string(src)) + "</pre>") //nolint:gosec
		}
		data := FileData{
			Title:       "📄 " + fileName,
			CurrentPath: reqPath,
			Breadcrumbs: buildBreadcrumbs(reqPath),
			Content:     rendered,
			RawPath:     "/raw" + reqPath,
			IsMarkdown:  false,
			IsText:      true,
			FileName:    fileName,
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.ExecuteTemplate(w, "file.html", data); err != nil {
			log.Printf("template error: %v", err)
		}
		return
	}

	// Binary or unknown file – serve as download/inline
	f, err := os.Open(fullPath)
	if err != nil {
		http.Error(w, "Cannot open file", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	mimeType := mime.TypeByExtension(filepath.Ext(fileName))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Disposition", "inline; filename="+url.PathEscape(fileName))
	io.Copy(w, f) //nolint:errcheck
}

func handleRaw(w http.ResponseWriter, r *http.Request) {
	reqPath := strings.TrimPrefix(r.URL.Path, "/raw")
	if reqPath == "" {
		reqPath = "/"
	}

	fullPath, err := safePath(reqPath)
	if err != nil {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	if info.IsDir() {
		http.Error(w, "Cannot download directory", http.StatusBadRequest)
		return
	}

	fileName := filepath.Base(fullPath)
	w.Header().Set("Content-Disposition", "attachment; filename="+url.PathEscape(fileName))
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, fullPath)
}

func handleBrowse(w http.ResponseWriter, r *http.Request) {
	reqPath := r.URL.Path
	if reqPath == "" {
		reqPath = "/"
	}

	fullPath, err := safePath(reqPath)
	if err != nil {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	if info.IsDir() {
		handleDir(w, r, fullPath, reqPath)
	} else {
		handleFile(w, r, fullPath, reqPath)
	}
}

func main() {
	port := flag.String("port", "8080", "Port to listen on")
	root := flag.String("root", "", "Root directory to serve (default: current directory)")
	flag.Parse()

	if *root != "" {
		rootDir = *root
	} else if envRoot := os.Getenv("ROOT_DIR"); envRoot != "" {
		rootDir = envRoot
	} else {
		var err error
		rootDir, err = os.Getwd()
		if err != nil {
			log.Fatal("Cannot get working directory:", err)
		}
	}

	// Resolve to absolute path
	var err error
	rootDir, err = filepath.Abs(rootDir)
	if err != nil {
		log.Fatal("Cannot resolve root directory:", err)
	}

	info, err := os.Stat(rootDir)
	if err != nil || !info.IsDir() {
		log.Fatalf("Root directory %q does not exist or is not a directory", rootDir)
	}

	initMarkdown()
	initTemplates()

	mux := http.NewServeMux()

	// Serve static assets
	staticHandler := http.FileServer(http.FS(staticFS))
	mux.Handle("/static/", staticHandler)

	// Raw file download
	mux.HandleFunc("/raw/", handleRaw)

	// Browse handler (everything else)
	mux.HandleFunc("/", handleBrowse)

	addr := ":" + *port
	log.Printf("WorkSpace file browser started")
	log.Printf("Serving: %s", rootDir)
	log.Printf("Listening on http://localhost%s", addr)

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
