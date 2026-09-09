package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mopeyjellyfish/flixr/backend/catalog"
)

type setupPathState string

const (
	setupPathReady        setupPathState = "ready"
	setupPathMissing      setupPathState = "missing"
	setupPathNotDirectory setupPathState = "not_directory"
	setupPathUnreadable   setupPathState = "unreadable"
	setupPathUnwritable   setupPathState = "unwritable"
)

type setupCheck struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	State   string `json:"state"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
	Action  string `json:"action,omitempty"`
}

func probeSetupTool(ctx context.Context, path string) bool {
	return exec.CommandContext(ctx, path, "-version").Run() == nil
}

func probeSetupPath(path string, writable bool) setupPathState {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return setupPathMissing
	}
	if err != nil {
		return setupPathUnreadable
	}
	if !info.IsDir() {
		return setupPathNotDirectory
	}
	directory, err := os.Open(path)
	if err != nil {
		return setupPathUnreadable
	}
	_, readErr := directory.Readdirnames(1)
	closeErr := directory.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return setupPathUnreadable
	}
	if closeErr != nil {
		return setupPathUnreadable
	}
	if !writable {
		return setupPathReady
	}
	probe, err := os.CreateTemp(path, ".flixr-readiness-*")
	if err != nil {
		return setupPathUnwritable
	}
	name := probe.Name()
	if err := probe.Close(); err != nil {
		_ = os.Remove(name)
		return setupPathUnwritable
	}
	if err := os.Remove(name); err != nil {
		return setupPathUnwritable
	}
	return setupPathReady
}

func (s *Server) ownerSetup(w http.ResponseWriter, r *http.Request) {
	if !s.owner(w, r) {
		return
	}
	if r.Method == http.MethodPatch {
		var body struct {
			Step string `json:"step"`
		}
		if !decode(r, &body) || s.catalog.SetSetupProgress(body.Step) != nil {
			fail(w, http.StatusBadRequest, "invalid_setup_progress")
			return
		}
		write(w, http.StatusOK, map[string]string{"step": body.Step})
		return
	}
	s.checkReadiness()
	step, err := s.resolvedSetupProgress()
	if err != nil {
		fail(w, http.StatusInternalServerError, "setup_progress_failed")
		return
	}
	films, tv := s.catalog.Roots()
	if r.URL.Query().Has("films") {
		films = strings.TrimSpace(r.URL.Query().Get("films"))
	}
	if r.URL.Query().Has("tv") {
		tv = strings.TrimSpace(r.URL.Query().Get("tv"))
	}
	write(w, http.StatusOK, map[string]any{"step": step, "checks": s.setupChecks(r.Context(), films, tv)})
}

func (s *Server) resolvedSetupProgress() (string, error) {
	step, err := s.catalog.SetupProgress()
	if err != nil {
		return "", err
	}
	if len(s.house.Profiles()) > 0 {
		return catalog.SetupProgressComplete, nil
	}
	if step != "" {
		return step, nil
	}
	films, tv := s.catalog.Roots()
	if films != "" || tv != "" {
		return catalog.SetupProgressProfile, nil
	}
	return catalog.SetupProgressChoice, nil
}

func (s *Server) setupChecks(parent context.Context, films, tv string) []setupCheck {
	checks := make([]setupCheck, 0, 6)
	if path := s.settingsValues["server.data_dir"]; path != "" {
		checks = append(checks, s.pathCheck("data", "Server data", path, true))
	}
	cache := s.settingsValues["playback.segment_dir"]
	if s.playback != nil {
		cache = s.playback.Settings().SegmentDir
	}
	if cache != "" {
		checks = append(checks, s.pathCheck("cache", "Playback cache", cache, true))
	}
	checks = append(checks, s.pathCheck("films", "Films", films, false), s.pathCheck("tv", "TV", tv, false))
	s.readyMu.RLock()
	ready := s.readiness
	s.readyMu.RUnlock()
	ctx, cancel := context.WithTimeout(parent, s.readinessTimeout)
	defer cancel()
	checks = append(checks, toolCheck("ffprobe", s.toolReady(ctx, "ffprobe", ready.FFprobe)), toolCheck("ffmpeg", s.toolReady(ctx, "ffmpeg", ready.FFmpeg)))
	return checks
}

func (s *Server) toolReady(ctx context.Context, name string, available bool) bool {
	if !available {
		return false
	}
	path, err := s.lookPath(name)
	return err == nil && s.toolProbe(ctx, path)
}

func (s *Server) pathCheck(id, label, path string, writable bool) setupCheck {
	if path == "" {
		return setupCheck{ID: id, Label: label, State: "not_configured", Message: "No folder is configured yet."}
	}
	state := s.pathProbe(filepath.Clean(path), writable)
	check := setupCheck{ID: id, Label: label, State: string(state), Path: filepath.Clean(path)}
	switch state {
	case setupPathReady:
		if writable {
			check.Message = "Flixr can open and write this container folder."
		} else {
			check.Message = "Flixr can traverse and read this container folder."
		}
	case setupPathMissing:
		check.Message = "This folder does not exist inside the Flixr container."
		check.Action = "Add or correct the read-only media volume in your Compose file, then recheck this container path."
	case setupPathNotDirectory:
		check.Message = "This path points to a file instead of a folder."
		check.Action = "Enter the mounted folder path inside the container, such as /media/films."
	case setupPathUnwritable:
		check.Message = "Flixr can open this folder but cannot write to it."
		check.Action = "Give the Flixr container user write access to this data or cache volume, then recheck."
	default:
		check.Message = "Flixr cannot traverse or read this folder."
		check.Action = "Give the Flixr container user read and directory traversal access, then recheck."
	}
	return check
}

func toolCheck(name string, ready bool) setupCheck {
	label := name
	if name == "ffprobe" {
		label = "Media inspection"
	} else if name == "ffmpeg" {
		label = "Compatibility playback"
	}
	if ready {
		return setupCheck{ID: name, Label: label, State: "ready", Message: name + " is available in the Flixr container."}
	}
	return setupCheck{ID: name, Label: label, State: "unavailable", Message: name + " is unavailable in the Flixr container.", Action: "Use the official Flixr image or install the FFmpeg tools, restart Flixr, then recheck."}
}
