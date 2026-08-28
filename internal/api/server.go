package api

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/samcm/pixel-steward/internal/config"
	"github.com/samcm/pixel-steward/internal/controller"
	"github.com/samcm/pixel-steward/internal/domain"
)

//go:embed ui.html
var userInterface []byte

type Server struct {
	service       *controller.Service
	operatorToken string
	mux           *http.ServeMux
}

func NewServer(service *controller.Service, cfg config.HTTP) (*Server, error) {
	server := &Server{service: service, mux: http.NewServeMux()}
	if cfg.Auth.Mode == "bearer" {
		value, err := os.ReadFile(cfg.Auth.TokenFile)
		if err != nil {
			return nil, fmt.Errorf("read operator token: %w", err)
		}
		server.operatorToken = strings.TrimSpace(string(value))
		if server.operatorToken == "" {
			return nil, errors.New("operator token file is empty")
		}
	}
	server.routes()
	return server, nil
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /", func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = response.Write(userInterface)
	})
	s.mux.Handle("GET /api/v1/status", s.operator(http.HandlerFunc(s.status)))
	s.mux.Handle("GET /api/v1/personas", s.operator(http.HandlerFunc(s.personas)))
	s.mux.Handle("GET /api/v1/personas/{id}", s.operator(http.HandlerFunc(s.persona)))
	s.mux.Handle("PUT /api/v1/personas/{id}/enabled", s.operator(http.HandlerFunc(s.personaEnabled)))
	s.mux.Handle("POST /api/v1/lease/revoke", s.operator(http.HandlerFunc(s.revoke)))
	s.mux.Handle("PUT /api/v1/lease/reasoning", s.operator(http.HandlerFunc(s.reasoning)))
	s.mux.Handle("GET /api/v1/leases", s.operator(http.HandlerFunc(s.leases)))
	s.mux.Handle("GET /api/v1/events", s.operator(http.HandlerFunc(s.events)))
	s.mux.Handle("GET /api/v1/frames", s.operator(http.HandlerFunc(s.frames)))
	s.mux.Handle("GET /api/v1/inference", s.operator(http.HandlerFunc(s.inference)))
	s.mux.Handle("GET /api/v1/preview.png", s.operator(http.HandlerFunc(s.preview)))
	s.mux.Handle("GET /api/v1/objects", s.operator(http.HandlerFunc(s.object)))

	s.mux.HandleFunc("GET /agent/v1/budget", s.agentBudget)
	s.mux.HandleFunc("POST /agent/v1/sql", s.agentSQL)
	s.mux.HandleFunc("POST /agent/v1/exec", s.agentExec)
	s.mux.HandleFunc("POST /agent/v1/publish", s.agentPublish)
	s.mux.HandleFunc("POST /agent/v1/watch", s.agentWatch)
	s.mux.HandleFunc("POST /agent/v1/schedules", s.agentSchedule)
}

func (s *Server) status(response http.ResponseWriter, request *http.Request) {
	value, err := s.service.Status(request.Context())
	writeJSON(response, value, err)
}

func (s *Server) personas(response http.ResponseWriter, request *http.Request) {
	value, err := s.service.Personas(request.Context())
	writeJSON(response, value, err)
}

func (s *Server) persona(response http.ResponseWriter, request *http.Request) {
	value, err := s.service.PersonaDetail(request.Context(), request.PathValue("id"))
	writeJSON(response, value, err)
}

func (s *Server) personaEnabled(response http.ResponseWriter, request *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := decode(request, &body); err != nil {
		writeJSON(response, nil, err)
		return
	}
	err := s.service.SetPersonaEnabled(request.Context(), request.PathValue("id"), body.Enabled)
	writeJSON(response, map[string]bool{"enabled": body.Enabled}, err)
}

func (s *Server) revoke(response http.ResponseWriter, request *http.Request) {
	err := s.service.Revoke(request.Context())
	writeJSON(response, map[string]bool{"revoked": err == nil}, err)
}

func (s *Server) reasoning(response http.ResponseWriter, request *http.Request) {
	var body struct {
		Value string `json:"value"`
	}
	if err := decode(request, &body); err != nil {
		writeJSON(response, nil, err)
		return
	}
	err := s.service.SetThinking(request.Context(), body.Value)
	writeJSON(response, map[string]string{"effective": body.Value}, err)
}

func (s *Server) leases(response http.ResponseWriter, request *http.Request) {
	value, err := s.service.Store().ListLeases(request.Context(), limit(request))
	writeJSON(response, value, err)
}

func (s *Server) events(response http.ResponseWriter, request *http.Request) {
	value, err := s.service.Store().ListEvents(request.Context(), limit(request))
	writeJSON(response, value, err)
}

func (s *Server) frames(response http.ResponseWriter, request *http.Request) {
	value, err := s.service.Store().ListFrames(request.Context(), request.URL.Query().Get("lease_id"), limit(request))
	writeJSON(response, value, err)
}

func (s *Server) inference(response http.ResponseWriter, request *http.Request) {
	value, err := s.service.Store().ListInferenceRequests(request.Context(), request.URL.Query().Get("lease_id"), limit(request))
	writeJSON(response, value, err)
}

func (s *Server) preview(response http.ResponseWriter, request *http.Request) {
	frames, err := s.service.Store().ListFrames(request.Context(), "", 1)
	if err != nil || len(frames) == 0 {
		http.Error(response, "no frame", http.StatusNotFound)
		return
	}
	s.writeObject(response, request, frames[0].FinalObject)
}

func (s *Server) object(response http.ResponseWriter, request *http.Request) {
	s.writeObject(response, request, request.URL.Query().Get("key"))
}

func (s *Server) writeObject(response http.ResponseWriter, request *http.Request, key string) {
	reader, object, err := s.service.Objects().Get(request.Context(), key)
	if err != nil {
		http.Error(response, err.Error(), http.StatusNotFound)
		return
	}
	defer reader.Close()
	contentType := object.ContentType
	if contentType == "" && strings.HasSuffix(strings.ToLower(key), ".png") {
		contentType = "image/png"
	}
	response.Header().Set("Content-Type", contentType)
	response.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	_, _ = io.Copy(response, reader)
}

func (s *Server) agentBudget(response http.ResponseWriter, request *http.Request) {
	snapshot, lease, err := s.service.Budget(request.Context(), bearer(request))
	writeJSON(response, map[string]any{"lease_id": lease.ID, "lease_end": lease.EndsAt, "reasoning": map[string]string{
		"effective": lease.Thinking, "source": "controller_config"}, "blackout": s.service.Blackout(), "enforcement": "controller", "accounting": snapshot}, err)
}

func (s *Server) agentSQL(response http.ResponseWriter, request *http.Request) {
	var body struct {
		Query string `json:"query"`
	}
	if err := decode(request, &body); err != nil {
		writeJSON(response, nil, err)
		return
	}
	value, err := s.service.QueryHistory(request.Context(), bearer(request), body.Query)
	writeJSON(response, value, err)
}

func (s *Server) agentExec(response http.ResponseWriter, request *http.Request) {
	var body struct {
		Command   string `json:"command"`
		TimeoutMS int64  `json:"timeout_ms"`
	}
	if err := decode(request, &body); err != nil {
		writeJSON(response, nil, err)
		return
	}
	value, err := s.service.Exec(request.Context(), bearer(request), body.Command, time.Duration(body.TimeoutMS)*time.Millisecond)
	writeJSON(response, value, err)
}

func (s *Server) agentPublish(response http.ResponseWriter, request *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if err := decode(request, &body); err != nil {
		writeJSON(response, nil, err)
		return
	}
	value, err := s.service.PublishPath(request.Context(), bearer(request), body.Path, true)
	writeJSON(response, value, err)
}

func (s *Server) agentWatch(response http.ResponseWriter, request *http.Request) {
	var body struct {
		Path string  `json:"path"`
		FPS  float64 `json:"fps"`
	}
	if err := decode(request, &body); err != nil {
		writeJSON(response, nil, err)
		return
	}
	value, err := s.service.WatchRenderer(request.Context(), bearer(request), body.Path, body.FPS)
	writeJSON(response, value, err)
}

func (s *Server) agentSchedule(response http.ResponseWriter, request *http.Request) {
	var body struct {
		Label        string          `json:"label"`
		RunAt        time.Time       `json:"run_at"`
		IntervalSecs int64           `json:"interval_seconds"`
		MissedPolicy string          `json:"missed_policy"`
		Payload      json.RawMessage `json:"payload"`
	}
	if err := decode(request, &body); err != nil {
		writeJSON(response, nil, err)
		return
	}
	value, err := s.service.CreateSchedule(request.Context(), bearer(request), domain.Schedule{Kind: "model_wake",
		Label: body.Label, RunAt: body.RunAt, Interval: time.Duration(body.IntervalSecs) * time.Second,
		MissedPolicy: body.MissedPolicy, Payload: body.Payload})
	writeJSON(response, value, err)
}

func (s *Server) operator(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if s.operatorToken != "" && bearer(request) != s.operatorToken {
			http.Error(response, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(response, request)
	})
}

func bearer(request *http.Request) string {
	value := request.Header.Get("Authorization")
	if strings.HasPrefix(value, "Bearer ") {
		return strings.TrimPrefix(value, "Bearer ")
	}
	return ""
}

func decode(request *http.Request, destination any) error {
	decoder := json.NewDecoder(io.LimitReader(request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	return decoder.Decode(destination)
}

func limit(request *http.Request) int {
	value, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	if value <= 0 || value > 1000 {
		value = 100
	}
	return value
}

func writeJSON(response http.ResponseWriter, value any, err error) {
	response.Header().Set("Content-Type", "application/json")
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, controller.ErrUnauthorized) || errors.Is(err, controller.ErrLeaseExpired) {
			status = http.StatusUnauthorized
		}
		response.WriteHeader(status)
		_ = json.NewEncoder(response).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(response).Encode(value)
}

func Shutdown(ctx context.Context, server *http.Server) error { return server.Shutdown(ctx) }
