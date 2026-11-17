package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jose00735/macawlink/windows/internal/app"
	"github.com/jose00735/macawlink/windows/internal/config"
)

type fakeAPI struct{ s config.Settings }

func (f *fakeAPI) Status() app.Status        { return app.Status{Phase: "streaming"} }
func (f *fakeAPI) Settings() config.Settings { return f.s }
func (f *fakeAPI) ApplySettings(s config.Settings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	f.s = s
	return nil
}
func (f *fakeAPI) ApplyPreset(n string) error { f.s = config.Presets[n]; return nil }
func (f *fakeAPI) RequestKeyframe()           {}

func do(h http.Handler, method, url, host, body string, hdr bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, url, strings.NewReader(body))
	r.Host = host
	if hdr {
		r.Header.Set("X-Macawlink", "1")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestHandler(t *testing.T) {
	api := &fakeAPI{s: config.Default()}
	h := Handler(api)
	if w := do(h, "GET", "/", "127.0.0.1:47801", "", false); w.Code != 200 || !strings.Contains(w.Body.String(), "Macaw") {
		t.Fatalf("index: %d", w.Code)
	}
	if w := do(h, "GET", "/api/state", "127.0.0.1:47801", "", false); w.Code != 200 || !strings.Contains(w.Body.String(), `"resolutions"`) {
		t.Fatalf("state: %d %s", w.Code, w.Body)
	}
	// DNS-rebinding and cross-site protections.
	if w := do(h, "GET", "/api/state", "evil.example:47801", "", false); w.Code != 403 {
		t.Fatalf("foreign host allowed: %d", w.Code)
	}
	if w := do(h, "POST", "/api/preset/low", "127.0.0.1:47801", "", false); w.Code != 403 {
		t.Fatalf("POST without header allowed: %d", w.Code)
	}
	if w := do(h, "POST", "/api/preset/low", "127.0.0.1:47801", "", true); w.Code != 200 || api.s.Video.Width != 640 {
		t.Fatalf("preset: %d", w.Code)
	}
	bad := `{"video":{"width":999,"height":1,"fps":30,"codec":"h264","rate_control":"cbr","lens":"back","zoom":1},"audio":{"codec":"pcm","sample_rate":48000,"channels":1,"frame_ms":10}}`
	if w := do(h, "POST", "/api/settings", "localhost:47801", bad, true); w.Code != 422 {
		t.Fatalf("invalid settings accepted: %d", w.Code)
	}
}
