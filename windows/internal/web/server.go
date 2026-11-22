// Package web serves the local settings page (http://127.0.0.1:47801) opened from the tray.
package web

import (
	"embed"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/jose00735/macawlink/windows/internal/app"
	"github.com/jose00735/macawlink/windows/internal/config"
	"github.com/jose00735/macawlink/windows/internal/pairing"
)

//go:embed assets/*
var assets embed.FS

const DefaultAddr = "127.0.0.1:47801"

type API interface {
	Status() app.Status
	Settings() config.Settings
	ApplySettings(config.Settings) error
	ApplyPreset(string) error
	RequestKeyframe()
	PairDecision(accept bool) error
	PairedPhones() []pairing.Phone
	ForgetPhone(id string)
	Prompter() app.PrompterInfo
	SetPrompterScript([]byte)
	SetPrompterState([]byte)
	ClearPrompter()
	PrompterKeys() []string
}

type options struct {
	Resolutions [][2]int `json:"resolutions"`
	FrameRates  []int    `json:"frame_rates"`
	SampleRates []int    `json:"sample_rates"`
	Presets     []string `json:"presets"`
}

type state struct {
	Paired   []pairing.Phone `json:"paired"`
	Status   app.Status      `json:"status"`
	Settings config.Settings `json:"settings"`
	Options  options         `json:"options"`
}

// Handler returns the settings UI + JSON API. POSTs need the X-Macawlink header (blocks
// cross-site form posts) and every request must target localhost (blocks DNS rebinding).
func Handler(api API) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		serveAsset(w, "assets/index.html", "text/html; charset=utf-8")
	})
	mux.HandleFunc("GET /icon.svg", func(w http.ResponseWriter, r *http.Request) {
		serveAsset(w, "assets/icon.svg", "image/svg+xml")
	})
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, state{
			Status:   api.Status(),
			Paired:   api.PairedPhones(),
			Settings: api.Settings(),
			Options: options{Resolutions: config.Resolutions, FrameRates: config.FrameRates,
				SampleRates: config.SampleRates, Presets: config.PresetNames},
		})
	})
	mux.HandleFunc("POST /api/settings", func(w http.ResponseWriter, r *http.Request) {
		var s config.Settings
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&s); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := api.ApplySettings(s); err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, api.Settings())
	})
	mux.HandleFunc("POST /api/preset/{name}", func(w http.ResponseWriter, r *http.Request) {
		if err := api.ApplyPreset(r.PathValue("name")); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, api.Settings())
	})
	mux.HandleFunc("POST /api/pairing/{answer}", func(w http.ResponseWriter, r *http.Request) {
		if err := api.PairDecision(r.PathValue("answer") == "accept"); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/paired/{id}/forget", func(w http.ResponseWriter, r *http.Request) {
		api.ForgetPhone(r.PathValue("id"))
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	// Teleprompter feed from TeleCyborg (github.com/jose00735/telecyborg) on this PC: the text is
	// shown over the camera preview on the phone screen, never in the video stream.
	mux.HandleFunc("GET /api/prompter", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, api.Prompter())
	})
	prompterBody := func(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
		b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil || !json.Valid(b) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body must be JSON (max 1 MiB)"})
			return nil, false
		}
		return b, true
	}
	mux.HandleFunc("POST /api/prompter/script", func(w http.ResponseWriter, r *http.Request) {
		if b, ok := prompterBody(w, r); ok {
			api.SetPrompterScript(b)
			writeJSON(w, http.StatusOK, api.Prompter())
		}
	})
	mux.HandleFunc("POST /api/prompter/state", func(w http.ResponseWriter, r *http.Request) {
		if b, ok := prompterBody(w, r); ok {
			api.SetPrompterState(b)
			writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		}
	})
	mux.HandleFunc("POST /api/prompter/clear", func(w http.ResponseWriter, r *http.Request) {
		api.ClearPrompter()
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/prompter/keys", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"keys": api.PrompterKeys()})
	})
	mux.HandleFunc("POST /api/keyframe", func(w http.ResponseWriter, r *http.Request) {
		api.RequestKeyframe()
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	return guard(mux)
}

func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if host != "127.0.0.1" && host != "localhost" && host != "[::1]" && host != "::1" {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Header.Get("X-Macawlink") != "1" {
			http.Error(w, "missing X-Macawlink header", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; img-src 'self' data:")
		next.ServeHTTP(w, r)
	})
}

func serveAsset(w http.ResponseWriter, name, ctype string) {
	b, err := assets.ReadFile(name)
	if err != nil {
		http.NotFound(w, nil)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Write(b)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	enc.Encode(v)
}

// IsLocal reports whether addr only listens on loopback.
func IsLocal(addr string) bool {
	return strings.HasPrefix(addr, "127.0.0.1:") || strings.HasPrefix(addr, "localhost:")
}
