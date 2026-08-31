package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/samcm/pixel-steward/internal/domain"
	"github.com/samcm/pixel-steward/internal/frame"
)

type RendererOptions struct {
	Path            string
	FPS             float64
	ClipFrames      int
	FrameDelay      time.Duration
	RefreshInterval time.Duration
}

type rendererPayload struct {
	Path           string `json:"path"`
	ClipFrames     int    `json:"clip_frames,omitempty"`
	FrameDelayMS   int64  `json:"frame_delay_ms,omitempty"`
	RefreshSeconds int64  `json:"refresh_seconds,omitempty"`
}

type liveClip struct {
	frames        []*image.Paletted
	lastHash      string
	lastPublished time.Time
	lastAttempt   time.Time
}

func (s *Service) previewPathProcessed(ctx context.Context, token, path string) (domain.Frame, frame.Processed, error) {
	lease, err := s.authorize(ctx, token)
	if err != nil {
		return domain.Frame{}, frame.Processed{}, err
	}
	if s.inBlackout(s.clock()) {
		return domain.Frame{}, frame.Processed{}, ErrBlackout
	}
	source, contentType, err := s.executor.ReadFile(ctx, lease.ID, path)
	if err != nil {
		return domain.Frame{}, frame.Processed{}, err
	}
	data, err := io.ReadAll(io.LimitReader(source, frame.MaxInput+1))
	closeErr := source.Close()
	if err != nil {
		return domain.Frame{}, frame.Processed{}, err
	}
	if closeErr != nil {
		return domain.Frame{}, frame.Processed{}, closeErr
	}
	if len(data) > frame.MaxInput {
		return domain.Frame{}, frame.Processed{}, fmt.Errorf("frame exceeds %d-byte input limit", frame.MaxInput)
	}
	processed, err := frame.Process(bytes.NewReader(data))
	if err != nil {
		return domain.Frame{}, frame.Processed{}, err
	}
	record, err := s.publish(ctx, token, contentType, bytes.NewReader(data), false, false)
	return record, processed, err
}

func (s *Service) WatchRenderer(ctx context.Context, token string, options RendererOptions) (domain.Schedule, error) {
	if options.FPS <= 0 || options.FPS > s.config.Display.MaxFPS {
		return domain.Schedule{}, fmt.Errorf("fps must be greater than zero and at most %.3g", s.config.Display.MaxFPS)
	}
	if !validRendererPath(options.Path) {
		return domain.Schedule{}, errors.New("path must be workspace-relative and cannot contain '..'")
	}
	settings, err := s.rendererSettings(rendererPayload{Path: options.Path, ClipFrames: options.ClipFrames,
		FrameDelayMS: options.FrameDelay.Milliseconds(), RefreshSeconds: int64(options.RefreshInterval.Seconds())})
	if err != nil {
		return domain.Schedule{}, err
	}
	lease, err := s.authorize(ctx, token)
	if err != nil {
		return domain.Schedule{}, err
	}
	now := s.clock()
	interval := time.Duration(float64(time.Second) / options.FPS)
	payload := jsonValue(settings)
	existing, err := s.store.ListSchedules(ctx, lease.ID, nil)
	if err != nil {
		return domain.Schedule{}, err
	}
	for _, schedule := range existing {
		if !schedule.Enabled || schedule.Kind != "renderer" {
			continue
		}
		var current rendererPayload
		if json.Unmarshal(schedule.Payload, &current) == nil && current == settings && schedule.Interval == interval {
			return schedule, nil
		}
		if err := s.store.MarkScheduleRun(ctx, lease.ID, schedule.ID, now, nil); err != nil {
			return domain.Schedule{}, err
		}
		s.mu.Lock()
		delete(s.liveClips, schedule.ID)
		s.mu.Unlock()
	}
	schedule := domain.Schedule{Kind: "renderer", Label: "renderer:" + options.Path, RunAt: now.Add(interval), Interval: interval,
		MissedPolicy: "skip", Payload: payload}
	return s.CreateSchedule(ctx, token, schedule)
}

// reconcileRendererSchedules upgrades historic leases to the single-live-canvas
// contract. Older clients could accidentally create duplicate watches, and
// accepted absolute paths that the executor would reject on every tick.
func (s *Service) reconcileRendererSchedules(ctx context.Context, lease domain.Lease, now time.Time) error {
	s.mu.Lock()
	if s.reconciled[lease.ID] {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	schedules, err := s.store.ListSchedules(ctx, lease.ID, nil)
	if err != nil {
		return err
	}
	slices.SortFunc(schedules, func(a, b domain.Schedule) int { return b.RunAt.Compare(a.RunAt) })
	kept := false
	for _, schedule := range schedules {
		if !schedule.Enabled || schedule.Kind != "renderer" {
			continue
		}
		var payload rendererPayload
		valid := json.Unmarshal(schedule.Payload, &payload) == nil && validRendererPath(payload.Path)
		if valid && !kept {
			kept = true
			continue
		}
		if err := s.store.MarkScheduleRun(ctx, lease.ID, schedule.ID, now, nil); err != nil {
			return err
		}
		reason := "invalid_path"
		if valid {
			reason = "superseded"
		}
		s.event(ctx, domain.Event{At: now, LeaseID: lease.ID, PersonaID: lease.PersonaID, Actor: "controller", Type: "renderer.schedule.disabled", Payload: jsonValue(map[string]any{
			"schedule_id": schedule.ID, "reason": reason,
		})})
	}
	s.mu.Lock()
	s.reconciled[lease.ID] = true
	s.mu.Unlock()
	return nil
}

// ensureDisplayActive makes proxy restarts invisible to the show. The last
// successful explicit or generated commit is durable, so an unexpectedly dark
// panel can be rehydrated without spending inference or creating a new scene.
func (s *Service) ensureDisplayActive(ctx context.Context, lease domain.Lease, now time.Time) error {
	s.mu.Lock()
	if !s.displayProbe.IsZero() && now.Sub(s.displayProbe) < s.config.Display.Live.RestorePollInterval.Duration() {
		s.mu.Unlock()
		return nil
	}
	s.displayProbe = now
	s.mu.Unlock()
	status, err := s.display.Status(ctx)
	if err != nil {
		return err
	}
	if !status.Online {
		return errors.New("display is offline")
	}
	if status.ScreenOn {
		return nil
	}
	latest, err := s.store.LatestPublishedFrame(ctx, lease.ID)
	if err != nil || latest == nil {
		return err
	}
	asset, contentType, err := s.restoreAsset(ctx, *latest)
	if err != nil {
		return err
	}
	if err := s.display.Publish(ctx, asset, contentType, 0); err != nil {
		return err
	}
	s.event(ctx, domain.Event{At: now, LeaseID: lease.ID, PersonaID: lease.PersonaID, Actor: "controller", Type: "display.scene.restored", Payload: jsonValue(map[string]any{
		"frame_id": latest.ID, "sequence": latest.Sequence, "content_type": contentType,
	})})
	return nil
}

func (s *Service) restoreAsset(ctx context.Context, record domain.Frame) ([]byte, string, error) {
	source, _, err := s.objects.Get(ctx, record.SourceObject)
	if err != nil {
		return nil, "", err
	}
	data, readErr := io.ReadAll(io.LimitReader(source, frame.MaxInput+1))
	closeErr := source.Close()
	if readErr != nil || closeErr != nil {
		return nil, "", errors.Join(readErr, closeErr)
	}
	if len(data) > frame.MaxInput {
		return nil, "", fmt.Errorf("stored display asset exceeds %d bytes", frame.MaxInput)
	}
	if bytes.HasPrefix(data, []byte("GIF8")) && s.config.Display.Live.ClipFrames > 1 {
		return data, "image/gif", nil
	}
	final, _, err := s.objects.Get(ctx, record.FinalObject)
	if err != nil {
		return nil, "", err
	}
	defer final.Close()
	data, err = io.ReadAll(io.LimitReader(final, frame.MaxInput+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > frame.MaxInput {
		return nil, "", fmt.Errorf("stored display asset exceeds %d bytes", frame.MaxInput)
	}
	return data, "image/png", nil
}

func (s *Service) runRenderer(ctx context.Context, lease domain.Lease, schedule domain.Schedule, now time.Time) error {
	var payload rendererPayload
	if err := json.Unmarshal(schedule.Payload, &payload); err != nil || !validRendererPath(payload.Path) {
		_ = s.store.MarkScheduleRun(ctx, lease.ID, schedule.ID, now, nil)
		return errors.New("renderer schedule has invalid path")
	}
	settings, err := s.rendererSettings(payload)
	if err != nil {
		_ = s.store.MarkScheduleRun(ctx, lease.ID, schedule.ID, now, nil)
		return err
	}
	s.mu.Lock()
	token := s.tokens[lease.ID]
	s.mu.Unlock()
	_, processed, previewErr := s.previewPathProcessed(ctx, token, settings.Path)
	var publishErr error
	if previewErr == nil {
		publishErr = s.updateLiveClip(ctx, token, schedule, settings, processed, now)
	}
	next := now.Add(schedule.Interval)
	if !next.Before(lease.EndsAt) {
		next = time.Time{}
	}
	var nextPointer *time.Time
	if !next.IsZero() {
		nextPointer = &next
	}
	markErr := s.store.MarkScheduleRun(ctx, lease.ID, schedule.ID, now, nextPointer)
	return errors.Join(previewErr, publishErr, markErr)
}

func (s *Service) rendererSettings(value rendererPayload) (rendererPayload, error) {
	if value.ClipFrames == 0 {
		value.ClipFrames = s.config.Display.Live.ClipFrames
	}
	if value.FrameDelayMS == 0 {
		value.FrameDelayMS = s.config.Display.Live.FrameDelay.Duration().Milliseconds()
	}
	if value.RefreshSeconds == 0 {
		value.RefreshSeconds = int64(s.config.Display.Live.RefreshInterval.Duration().Seconds())
	}
	if value.ClipFrames < 1 || value.ClipFrames > s.config.Display.Live.ClipFrames {
		return rendererPayload{}, fmt.Errorf("clip_frames must be between 1 and the controller limit of %d", s.config.Display.Live.ClipFrames)
	}
	delay := time.Duration(value.FrameDelayMS) * time.Millisecond
	if delay < 50*time.Millisecond || delay > time.Minute {
		return rendererPayload{}, errors.New("frame_delay_ms must be between 50 and 60000")
	}
	refresh := time.Duration(value.RefreshSeconds) * time.Second
	if refresh < s.config.Display.Live.MinimumRefresh.Duration() {
		return rendererPayload{}, fmt.Errorf("refresh_seconds must be at least %.0f", s.config.Display.Live.MinimumRefresh.Duration().Seconds())
	}
	return value, nil
}

func validRendererPath(path string) bool {
	return path != "" && !filepath.IsAbs(path) && !strings.Contains(path, "..") && !strings.ContainsRune(path, '\x00')
}

func (s *Service) updateLiveClip(ctx context.Context, token string, schedule domain.Schedule, settings rendererPayload, processed frame.Processed, now time.Time) error {
	s.mu.Lock()
	state := s.liveClips[schedule.ID]
	if state == nil {
		state = &liveClip{}
		s.liveClips[schedule.ID] = state
	}
	if state.lastHash == processed.SHA256 {
		s.mu.Unlock()
		return nil
	}
	state.lastHash = processed.SHA256
	state.frames = append(state.frames, palettedFrame(processed.RGB))
	if len(state.frames) > settings.ClipFrames {
		state.frames = state.frames[len(state.frames)-settings.ClipFrames:]
	}
	refresh := time.Duration(settings.RefreshSeconds) * time.Second
	retry := min(time.Minute, refresh)
	ready := len(state.frames) == settings.ClipFrames &&
		(state.lastPublished.IsZero() || now.Sub(state.lastPublished) >= refresh) &&
		(state.lastAttempt.IsZero() || now.Sub(state.lastAttempt) >= retry)
	if !ready {
		s.mu.Unlock()
		return nil
	}
	frames := slices.Clone(state.frames)
	state.lastAttempt = now
	s.mu.Unlock()

	asset := bytes.Clone(processed.PNG)
	contentType := "image/png"
	if len(frames) > 1 {
		var err error
		asset, err = encodeResidentGIF(frames, time.Duration(settings.FrameDelayMS)*time.Millisecond)
		if err != nil {
			return err
		}
		contentType = "image/gif"
	}
	record, err := s.publish(ctx, token, contentType, bytes.NewReader(asset), false, true)
	if err != nil {
		return err
	}
	s.mu.Lock()
	state.lastPublished = now
	s.mu.Unlock()
	s.event(ctx, domain.Event{At: now, LeaseID: schedule.LeaseID, PersonaID: schedule.PersonaID, Actor: "controller", Type: "renderer.clip.committed", Payload: jsonValue(map[string]any{
		"schedule_id": schedule.ID, "frame_id": record.ID, "frames": len(frames), "frame_delay_ms": settings.FrameDelayMS,
		"content_type":    contentType,
		"refresh_seconds": settings.RefreshSeconds,
	})})
	return nil
}

func palettedFrame(rgb []byte) *image.Paletted {
	source := image.NewRGBA(image.Rect(0, 0, frame.Width, frame.Height))
	for index := 0; index < frame.Width*frame.Height && index*3+2 < len(rgb); index++ {
		offset := index * 3
		pixel := index * 4
		source.Pix[pixel] = rgb[offset]
		source.Pix[pixel+1] = rgb[offset+1]
		source.Pix[pixel+2] = rgb[offset+2]
		source.Pix[pixel+3] = 0xff
	}
	destination := image.NewPaletted(source.Bounds(), palette.Plan9)
	draw.FloydSteinberg.Draw(destination, destination.Rect, source, image.Point{})
	return destination
}

func encodeResidentGIF(frames []*image.Paletted, delay time.Duration) ([]byte, error) {
	if len(frames) < 2 || len(frames) > 8 {
		return nil, fmt.Errorf("resident animation needs 2-8 frames, got %d", len(frames))
	}
	delayCentiseconds := max(5, min(6000, int(delay/(10*time.Millisecond))))
	delays := make([]int, len(frames))
	for index := range delays {
		delays[index] = delayCentiseconds
	}
	var encoded bytes.Buffer
	if err := gif.EncodeAll(&encoded, &gif.GIF{Image: frames, Delay: delays, LoopCount: 0}); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}
