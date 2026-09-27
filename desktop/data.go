package main

import (
	"context"
	"errors"
	"time"

	"atlas/internal/hermes"
)

// DataService is the app's single bridge to head services. ALL HTTP happens
// here, on the Go side: no CORS in the webview, no bearer keys in the
// frontend, and the exact same client code the TUI uses (atlas/internal/hermes).
// This is also the seam where a head-side daemon slots in later without the
// frontend noticing.
type DataService struct {
	api *hermes.Client
	hub *hermes.Hub
}

func NewDataService() *DataService {
	return &DataService{api: hermes.NewFromEnv(), hub: hermes.NewHubFromEnv()}
}

// Status reports which upstreams are configured, for the status bar.
type Status struct {
	API    bool   `json:"api"`
	Hub    bool   `json:"hub"`
	APIURL string `json:"api_url"`
	HubURL string `json:"hub_url"`
}

func (d *DataService) Status() Status {
	return Status{
		API:    d.api.Configured(),
		Hub:    d.hub.Configured(),
		APIURL: d.api.BaseURL,
		HubURL: d.hub.BaseURL,
	}
}

// GetTree fetches the full workstream tree from the hub (all profiles).
func (d *DataService) GetTree() (*hermes.HubTree, error) {
	if !d.hub.Configured() {
		return nil, errors.New("hub not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return d.hub.FetchTree(ctx)
}

// GetSessions is the flat fallback list when the hub is unavailable.
func (d *DataService) GetSessions(limit int) ([]hermes.Session, error) {
	if !d.api.Configured() {
		return nil, errors.New("API server not configured")
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return d.api.ListSessions(ctx, limit)
}

// GetMessages reads one conversation's transcript.
func (d *DataService) GetMessages(profile, sessionID string, limit int) ([]hermes.Message, error) {
	if !d.api.ConfiguredFor(profile) {
		return nil, errors.New("no API key for profile " + profile)
	}
	if limit <= 0 || limit > 2000 {
		limit = 400
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return d.api.Messages(ctx, profile, sessionID, limit)
}

// SendMessage runs one full agent turn (blocking until the run completes).
// Streaming deltas come later; M1 keeps the turn synchronous.
func (d *DataService) SendMessage(profile, sessionID, text string) error {
	if !d.api.ConfiguredFor(profile) {
		return errors.New("no API key for profile " + profile)
	}
	if text == "" {
		return errors.New("empty message")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	return d.api.ChatStream(ctx, profile, sessionID, text, func(hermes.ChatEvent) {})
}
