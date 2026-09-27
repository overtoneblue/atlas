// relay.go — the filesystem edges of the chat, owned by the daemon.
//
// Two directions, one security model:
//   - POST /api/attach: pasted images land in a local inbox and come back
//     as absolute paths the agent can read (they ride the turn as MEDIA:
//     refs, so history re-renders them without carrying bytes).
//   - GET  /media?path=…: stored images (agent screenshots, pasted files,
//     skill assets) render in the transcript without exposing the
//     filesystem broadly.
//
// Remote shells (node0) relay to head via ATLAS_UPSTREAM — a tunneled
// loopback port — because that's where the agent's world lives; head
// itself runs local-only. Serving is restricted to absolute paths under
// the allowlisted roots, symlink-resolved, image extensions only.

package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxImageBytes = 8 << 20  // raw image cap (base64 ≈ 1.37× this on the wire)
const attachMaxBody = 12 << 20 // request body cap for /api/attach

var mimeExt = map[string]string{
	"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif",
	"image/webp": ".webp", "image/bmp": ".bmp",
}

var extMime = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp", ".bmp": "image/bmp",
}

func upstream() string { return strings.TrimRight(os.Getenv("ATLAS_UPSTREAM"), "/") }

func hermesHome() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "."
	}
	return filepath.Join(home, ".hermes")
}

// resolveMedia validates one /media path: absolute, existing (after symlink
// evaluation), under an allowlisted root, image extension.
func resolveMedia(p string) (string, error) {
	if p == "" || !filepath.IsAbs(p) {
		return "", errors.New("absolute path required")
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(p))
	if err != nil {
		return "", errors.New("not found")
	}
	for _, root := range []string{"/tmp", hermesHome()} {
		if resolved == root || strings.HasPrefix(resolved, root+string(os.PathSeparator)) {
			if _, ok := extMime[strings.ToLower(filepath.Ext(resolved))]; !ok {
				return "", errors.New("not an image")
			}
			return resolved, nil
		}
	}
	return "", errors.New("path outside allowed roots")
}

// media serves a stored image; on a remote shell the file usually lives on
// head, so an unresolvable path relays upstream (which re-validates).
func (a *api) media(w http.ResponseWriter, r *http.Request) {
	file, err := resolveMedia(r.URL.Query().Get("path"))
	if err == nil {
		http.ServeFile(w, r, file)
		return
	}
	if up := upstream(); up != "" {
		proxyTo(w, r, up)
		return
	}
	http.Error(w, "no such image", http.StatusNotFound)
}

// attach stores one pasted image (data URL) in the inbox and returns its
// path. On a remote shell it relays upstream first: the file must live
// where the agent does.
func (a *api) attach(w http.ResponseWriter, r *http.Request) {
	if up := upstream(); up != "" {
		proxyTo(w, r, up)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, attachMaxBody)
	var req struct {
		Data string `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("bad body"))
		return
	}
	path, err := saveDataURL(req.Data)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, map[string]string{"path": path})
}

func saveDataURL(d string) (string, error) {
	if !strings.HasPrefix(d, "data:") {
		return "", errors.New("expected a data URL")
	}
	head, payload, ok := strings.Cut(d[len("data:"):], ",")
	if !ok {
		return "", errors.New("malformed data URL")
	}
	mime, enc, ok := strings.Cut(head, ";")
	if !ok || !strings.EqualFold(enc, "base64") {
		return "", errors.New("expected base64 data URL")
	}
	ext, ok := mimeExt[strings.ToLower(mime)]
	if !ok {
		return "", fmt.Errorf("unsupported image type %q", mime)
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", errors.New("bad base64 payload")
	}
	if len(raw) > maxImageBytes {
		return "", fmt.Errorf("image too large (%d bytes, max %d)", len(raw), maxImageBytes)
	}
	dir := filepath.Join(hermesHome(), "cache", "atlas-inbox")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	var tag [4]byte
	_, _ = rand.Read(tag[:])
	full := filepath.Join(dir, fmt.Sprintf("paste-%d-%x%s", time.Now().Unix(), tag, ext))
	if err := os.WriteFile(full, raw, 0o644); err != nil {
		return "", err
	}
	return full, nil
}

// proxyTo forwards the current request to head's atlasd. Head runs the
// same validation, so a forwarded request can't widen the security model.
func proxyTo(w http.ResponseWriter, r *http.Request, base string) {
	u := base + r.URL.Path
	if r.URL.RawQuery != "" {
		u += "?" + r.URL.RawQuery
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, u, r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	resp, err := (&http.Client{Timeout: 90 * time.Second}).Do(req)
	if err != nil {
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
