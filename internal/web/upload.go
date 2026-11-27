package web

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
	"novapanel/internal/fileop"
	"novapanel/internal/id"
)

func (s *server) upload(w http.ResponseWriter, r *http.Request) {
	count := 0
	for _, c := range r.Cookies() {
		if c.Name == CookieName {
			count++
		}
	}
	if count > 1 {
		http.Error(w, "Ambiguous session cookie", http.StatusBadRequest)
		return
	}
	cookie, err := r.Cookie(CookieName)
	if err != nil || cookie.Value == "" {
		http.Error(w, "Session required", http.StatusUnauthorized)
		return
	}
	if !s.limiter.allow("session:"+cookie.Value, s.options.Now()) {
		w.Header().Set("Retry-After", "1")
		http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
		return
	}
	c, err := s.options.Dial()
	if err != nil {
		http.Error(w, "Agent unavailable", http.StatusServiceUnavailable)
		return
	}
	defer c.Close()
	v, ok := identity(w, r, c, "session.whoami", cookie.Value, view{})
	if !ok {
		return
	}
	if !s.sameOrigin(r) {
		if s.options.OriginViolation != nil {
			s.options.OriginViolation()
		}
		http.Error(w, "Cross-site request refused", http.StatusForbidden)
		return
	}
	values := r.Header.Values("X-CSRF-Token")
	if len(values) != 1 || values[0] == "" || subtle.ConstantTimeCompare([]byte(values[0]), []byte(v.CSRF)) != 1 {
		http.Error(w, "Invalid CSRF token", http.StatusForbidden)
		return
	}
	if v.Role != "owner" && v.Role != "admin" {
		http.Error(w, "forbidden: read-only account", http.StatusForbidden)
		return
	}
	controller := http.NewResponseController(w)
	if err := controller.SetReadDeadline(time.Now().Add(10 * time.Minute)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		http.Error(w, "Unable to set upload deadline", http.StatusInternalServerError)
		return
	}
	if err := controller.SetWriteDeadline(time.Now().Add(10*time.Minute + 20*time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		http.Error(w, "Unable to set upload deadline", http.StatusInternalServerError)
		return
	}
	uploadID, err := id.New()
	if err != nil {
		http.Error(w, "Unable to stage upload", http.StatusInternalServerError)
		return
	}
	temp := filepath.Join(s.options.UploadsDir, ".tmp-"+uploadID)
	fd, err := unix.Open(temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		http.Error(w, "Unable to stage upload", http.StatusInternalServerError)
		return
	}
	f := os.NewFile(uintptr(fd), temp)
	defer os.Remove(temp)
	_, err = io.Copy(f, http.MaxBytesReader(w, r.Body, fileop.MaxUpload))
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "Upload too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "Unable to stage upload", http.StatusInternalServerError)
		}
		return
	}
	if err := controller.SetWriteDeadline(time.Now().Add(20 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		http.Error(w, "Unable to set reply deadline", http.StatusInternalServerError)
		return
	}
	if err = os.Rename(temp, filepath.Join(s.options.UploadsDir, uploadID)); err != nil {
		http.Error(w, "Unable to stage upload", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{"upload_id": uploadID})
}
