package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Data package storage and the small slice of the TAK Server "Marti" HTTP API
// that clients use to upload, search and download data packages.

var errQuota = errors.New("storage quota exceeded")

var reHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

type pkgOut struct {
	UID                string   `json:"UID"`
	Name               string   `json:"Name"`
	Hash               string   `json:"Hash"`
	PrimaryKey         int      `json:"PrimaryKey"`
	SubmissionDateTime string   `json:"SubmissionDateTime"`
	SubmissionUser     string   `json:"SubmissionUser"`
	CreatorUid         string   `json:"CreatorUid"`
	Keywords           []string `json:"Keywords"`
	MIMEType           string   `json:"MIMEType"`
	Size               int64    `json:"Size"`
}

type rec struct {
	pkgOut
	Private bool `json:"private"`
}

type store struct {
	mu     sync.RWMutex
	dir    string
	maxTot int64
	byHash map[string]*rec
	byUID  map[string]*rec
	nextPK int
}

func openStore(dir string, maxTotal int64) (*store, error) {
	s := &store{dir: dir, maxTot: maxTotal, byHash: map[string]*rec{}, byUID: map[string]*rec{}, nextPK: 1}
	// Leftover temp files from an interrupted upload.
	if tmps, _ := filepath.Glob(filepath.Join(dir, "up-*.tmp")); len(tmps) > 0 {
		for _, t := range tmps {
			os.Remove(t)
		}
	}
	metas, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	for _, m := range metas {
		b, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		var r rec
		if json.Unmarshal(b, &r) != nil || !reHash.MatchString(r.Hash) {
			log.Printf("store: skipping unreadable %s", m)
			continue
		}
		if _, err := os.Stat(s.binPath(r.Hash)); err != nil {
			log.Printf("store: metadata without file, skipping %s", m)
			continue
		}
		s.byHash[r.Hash] = &r
		s.byUID[r.UID] = &r
		if r.PrimaryKey >= s.nextPK {
			s.nextPK = r.PrimaryKey + 1
		}
	}
	return s, nil
}

func (s *store) binPath(hash string) string  { return filepath.Join(s.dir, hash+".bin") }
func (s *store) metaPath(hash string) string { return filepath.Join(s.dir, hash+".json") }

func (s *store) totalLocked() int64 {
	var t int64
	for _, r := range s.byHash {
		t += r.Size
	}
	return t
}

func (s *store) used() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.totalLocked()
}

func (s *store) count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.byHash)
}

func (s *store) byHashGet(h string) *rec {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.byHash[strings.ToLower(h)]
}

func (s *store) byUIDGet(u string) *rec {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.byUID[u]
}

func (s *store) list(includePrivate bool) []*rec {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*rec, 0, len(s.byHash))
	for _, r := range s.byHash {
		if r.Private && !includePrivate {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SubmissionDateTime > out[j].SubmissionDateTime })
	return out
}

func (s *store) saveMeta(r *rec) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.metaPath(r.Hash) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.metaPath(r.Hash))
}

func (s *store) setPrivate(hash string, private bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.byHash[hash]
	if r == nil {
		return false
	}
	r.Private = private
	if err := s.saveMeta(r); err != nil {
		log.Printf("store: save %s: %v", hash, err)
	}
	return true
}

func newUUID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func nowStamp() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000Z") }

// commit moves a finished temp upload into place and records its metadata.
func (s *store) commit(tmp, hash string, size int64, name, mimeType, creator string, private bool) (*rec, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing := s.byHash[hash]; existing != nil {
		os.Remove(tmp)
		existing.Name, existing.CreatorUid, existing.SubmissionUser = name, creator, creator
		existing.Private = private
		existing.SubmissionDateTime = nowStamp()
		if mimeType != "" {
			existing.MIMEType = mimeType
		}
		if err := s.saveMeta(existing); err != nil {
			return nil, err
		}
		return existing, nil
	}
	if s.totalLocked()+size > s.maxTot {
		os.Remove(tmp)
		return nil, errQuota
	}
	if err := os.Rename(tmp, s.binPath(hash)); err != nil {
		os.Remove(tmp)
		return nil, err
	}
	var kw []string
	if strings.HasSuffix(strings.ToLower(name), ".zip") {
		kw = []string{"missionpackage"}
	}
	r := &rec{pkgOut: pkgOut{
		UID: newUUID(), Name: name, Hash: hash, PrimaryKey: s.nextPK,
		SubmissionDateTime: nowStamp(), SubmissionUser: creator, CreatorUid: creator,
		Keywords: kw, MIMEType: mimeType, Size: size,
	}, Private: private}
	s.nextPK++
	if err := s.saveMeta(r); err != nil {
		os.Remove(s.binPath(hash))
		return nil, err
	}
	s.byHash[hash] = r
	s.byUID[r.UID] = r
	return r, nil
}

// ---- HTTP ----

type api struct {
	st        *store
	hub       *hub
	maxUpload int64
}

type statusWriter struct {
	http.ResponseWriter
	code int
	n    int64
}

func (w *statusWriter) WriteHeader(c int) {
	w.code = c
	w.ResponseWriter.WriteHeader(c)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = 200
	}
	n, err := w.ResponseWriter.Write(b)
	w.n += int64(n)
	return n, err
}

func logged(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if sw.code == 0 {
			sw.code = 200
		}
		log.Printf("http: %s %s %s -> %d (%d B) ua=%q", r.RemoteAddr, r.Method, r.URL.RequestURI(), sw.code, sw.n, r.UserAgent())
	})
}

func newMux(a *api) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", a.page)

	mux.HandleFunc("POST /Marti/sync/upload", a.upload)
	mux.HandleFunc("PUT /Marti/sync/upload", a.upload)
	mux.HandleFunc("POST /Marti/sync/content", a.upload)
	mux.HandleFunc("PUT /Marti/sync/content", a.upload)
	mux.HandleFunc("POST /Marti/sync/missionupload", a.upload)
	mux.HandleFunc("PUT /Marti/sync/missionupload", a.upload)

	mux.HandleFunc("GET /Marti/sync/content", a.download)
	mux.HandleFunc("GET /Marti/sync/missionquery", a.download)
	mux.HandleFunc("GET /Marti/api/sync/metadata/{hash}/tool", a.downloadByPath)
	mux.HandleFunc("GET /Marti/api/sync/metadata/{hash}/content", a.downloadByPath)
	mux.HandleFunc("PUT /Marti/api/sync/metadata/{hash}/tool", a.setPrivacy)

	mux.HandleFunc("GET /Marti/sync/search", a.search)
	mux.HandleFunc("GET /Marti/api/sync/metadata/all", a.search)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("http: UNHANDLED %s %s content-type=%q length=%d", r.Method, r.URL.RequestURI(), r.Header.Get("Content-Type"), r.ContentLength)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	})
	return logged(mux)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func cleanName(n string) string {
	n = strings.ReplaceAll(n, "\\", "/")
	n = filepath.Base(n)
	n = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, n)
	if n == "." || n == "/" {
		return ""
	}
	if len(n) > 200 {
		n = n[:200]
	}
	return n
}

type uploadResp struct {
	UID                string `json:"UID"`
	SubmissionDateTime string `json:"SubmissionDateTime"`
	MIMEType           string `json:"MIMEType"`
	SubmissionUser     string `json:"SubmissionUser"`
	PrimaryKey         int    `json:"PrimaryKey"`
	Hash               string `json:"Hash"`
}

func (a *api) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, a.maxUpload)
	q := r.URL.Query()
	name := cleanName(firstNonEmpty(q.Get("filename"), q.Get("name")))
	creator := firstNonEmpty(q.Get("creatorUid"), "unknown")

	var src io.Reader = r.Body
	ct := r.Header.Get("Content-Type")
	mimeType := ct
	if mt, params, err := mime.ParseMediaType(ct); err == nil && strings.HasPrefix(mt, "multipart/") {
		mr := multipart.NewReader(r.Body, params["boundary"])
		src = nil
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			if part.FileName() != "" || part.FormName() == "assetfile" {
				src = part
				if name == "" {
					name = cleanName(part.FileName())
				}
				mimeType = part.Header.Get("Content-Type")
				break
			}
		}
		if src == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no file in multipart body"})
			return
		}
	}

	tmp, err := os.CreateTemp(a.st.dir, "up-*.tmp")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, h), src)
	tmp.Close()
	if err != nil {
		os.Remove(tmp.Name())
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "upload too large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "upload failed: " + err.Error()})
		return
	}
	hash := hex.EncodeToString(h.Sum(nil))
	if want := strings.ToLower(q.Get("hash")); want != "" && want != hash {
		log.Printf("http: client-supplied hash %s does not match computed %s (using computed)", want, hash)
	}
	if name == "" {
		name = hash
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	rc, err := a.st.commit(tmp.Name(), hash, size, name, mimeType, creator, q.Get("tool") == "private")
	if errors.Is(err, errQuota) {
		writeJSON(w, http.StatusInsufficientStorage, map[string]string{"error": "server storage is full"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	log.Printf("sync: stored %q (%s, %d bytes) from %s", rc.Name, hash[:12], rc.Size, creator)

	switch {
	case q.Get("redirect") == "1":
		http.Redirect(w, r, "/", http.StatusSeeOther)
	case strings.HasSuffix(r.URL.Path, "/missionupload"):
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(w, "http://%s/Marti/api/sync/metadata/%s/tool", r.Host, rc.Hash)
	default:
		writeJSON(w, http.StatusOK, uploadResp{rc.UID, rc.SubmissionDateTime, rc.MIMEType, rc.SubmissionUser, rc.PrimaryKey, rc.Hash})
	}
}

func (a *api) serveRec(w http.ResponseWriter, r *http.Request, rc *rec) {
	f, err := os.Open(a.st.binPath(rc.Hash))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "file missing"})
		return
	}
	defer f.Close()
	st, _ := f.Stat()
	mt := rc.MIMEType
	if mt == "" {
		mt = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mt)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": rc.Name}))
	http.ServeContent(w, r, rc.Name, st.ModTime(), f)
}

func (a *api) download(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var rc *rec
	if h := q.Get("hash"); h != "" {
		rc = a.st.byHashGet(h)
	} else if u := q.Get("uid"); u != "" {
		rc = a.st.byUIDGet(u)
	}
	if rc == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such package"})
		return
	}
	a.serveRec(w, r, rc)
}

func (a *api) downloadByPath(w http.ResponseWriter, r *http.Request) {
	rc := a.st.byHashGet(r.PathValue("hash"))
	if rc == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such package"})
		return
	}
	a.serveRec(w, r, rc)
}

func (a *api) setPrivacy(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1024))
	v := strings.TrimSpace(string(body))
	if i := strings.Index(v, "="); i >= 0 {
		v = v[i+1:]
	}
	if !a.st.setPrivate(strings.ToLower(r.PathValue("hash")), v == "private") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such package"})
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	io.WriteString(w, "Okay")
}

func (a *api) search(w http.ResponseWriter, r *http.Request) {
	kw := strings.ToLower(r.URL.Query().Get("keyword"))
	all := a.st.list(false)
	res := make([]pkgOut, 0, len(all))
	for _, rc := range all {
		if kw != "" && kw != "missionpackage" {
			hit := strings.Contains(strings.ToLower(rc.Name), kw)
			for _, k := range rc.Keywords {
				if strings.Contains(strings.ToLower(k), kw) {
					hit = true
				}
			}
			if !hit {
				continue
			}
		}
		out := rc.pkgOut
		if out.Keywords == nil {
			out.Keywords = []string{}
		}
		res = append(res, out)
	}
	writeJSON(w, http.StatusOK, map[string]any{"resultCount": len(res), "results": res})
}

// ---- tiny web page (handy for checking from a phone browser) ----

func humanSize(n int64) string {
	const k = 1024
	switch {
	case n < k:
		return fmt.Sprintf("%d B", n)
	case n < k*k:
		return fmt.Sprintf("%.1f KB", float64(n)/k)
	case n < k*k*k:
		return fmt.Sprintf("%.1f MB", float64(n)/(k*k))
	}
	return fmt.Sprintf("%.2f GB", float64(n)/(k*k*k))
}

var pageTmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>puck-tak</title>
<style>
body{font:16px/1.5 system-ui,sans-serif;margin:0 auto;max-width:42rem;padding:1rem;background:#fff;color:#1a1a1a}
@media(prefers-color-scheme:dark){body{background:#161616;color:#e8e8e8}a{color:#8ab4ff}}
table{border-collapse:collapse;width:100%}td,th{text-align:left;padding:.3rem .5rem;border-bottom:1px solid #8884}
.m{color:#888;font-size:.9rem}
</style></head><body>
<h1>puck-tak</h1>
<p class="m">{{.Clients}} TAK client(s) connected · {{.Count}} package(s) · {{.Used}} of {{.Max}} used · v{{.Version}}</p>
<h2>Data packages</h2>
{{if .Rows}}<table><tr><th>Name</th><th>Size</th><th>Uploaded (UTC)</th></tr>
{{range .Rows}}<tr><td><a href="/Marti/sync/content?hash={{.Hash}}">{{.Name}}</a></td><td>{{.Size}}</td><td>{{.When}}</td></tr>
{{end}}</table>{{else}}<p class="m">Nothing uploaded yet.</p>{{end}}
<h2>Upload</h2>
<form method="post" action="/Marti/sync/upload?redirect=1&amp;creatorUid=web" enctype="multipart/form-data">
<input type="file" name="assetfile" required> <button type="submit">Upload</button></form>
</body></html>`))

func (a *api) page(w http.ResponseWriter, r *http.Request) {
	type row struct{ Name, Hash, Size, When string }
	var rows []row
	for _, rc := range a.st.list(false) {
		rows = append(rows, row{rc.Name, rc.Hash, humanSize(rc.Size), strings.Replace(strings.TrimSuffix(rc.SubmissionDateTime, "Z"), "T", " ", 1)})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	pageTmpl.Execute(w, map[string]any{
		"Clients": a.hub.clientCount(), "Count": a.st.count(), "Used": humanSize(a.st.used()),
		"Max": humanSize(a.st.maxTot), "Version": version, "Rows": rows,
	})
}
