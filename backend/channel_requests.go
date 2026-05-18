package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi"
	"github.com/redis/go-redis/v9"
)

type RequestStatus string

const (
	RequestStatusPending  RequestStatus = "pending"
	RequestStatusApproved RequestStatus = "approved"
	RequestStatusRejected RequestStatus = "rejected"
)

type ChannelRequest struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Email        string        `json:"email"`
	DesiredSlug  string        `json:"desiredSlug"`
	Description  string        `json:"description"`
	Status       RequestStatus `json:"status"`
	Notes        string        `json:"notes"`        // admin notes / rejection reason
	ApprovedSlug string        `json:"approvedSlug"` // final slug after approval
	CreatedAt    time.Time     `json:"createdAt"`
}

func dbSaveChannelRequest(ctx context.Context, req *ChannelRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if err := rdb.Set(ctx, "channel_request:"+req.ID, data, 0).Err(); err != nil {
		return err
	}
	return rdb.ZAdd(ctx, "channel_requests:list", redis.Z{
		Score:  float64(req.CreatedAt.Unix()),
		Member: req.ID,
	}).Err()
}

func dbGetChannelRequest(ctx context.Context, id string) (*ChannelRequest, error) {
	data, err := rdb.Get(ctx, "channel_request:"+id).Result()
	if err != nil {
		return nil, err
	}
	var req ChannelRequest
	if err := json.Unmarshal([]byte(data), &req); err != nil {
		return nil, err
	}
	return &req, nil
}

func dbListChannelRequests(ctx context.Context) ([]*ChannelRequest, error) {
	ids, err := rdb.ZRevRange(ctx, "channel_requests:list", 0, -1).Result()
	if err != nil {
		return nil, err
	}
	requests := make([]*ChannelRequest, 0, len(ids))
	for _, id := range ids {
		req, err := dbGetChannelRequest(ctx, id)
		if err != nil {
			continue
		}
		requests = append(requests, req)
	}
	return requests, nil
}

// POST /api/channel-request (public)
func submitChannelRequest(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var body struct {
		Name        string `json:"name"`
		Email       string `json:"email"`
		DesiredSlug string `json:"desiredSlug"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if body.Name == "" || body.Email == "" || body.DesiredSlug == "" {
		http.Error(w, "name, email, and desiredSlug are required", http.StatusBadRequest)
		return
	}

	req := &ChannelRequest{
		ID:          generatedRandomID(12),
		Name:        body.Name,
		Email:       body.Email,
		DesiredSlug: body.DesiredSlug,
		Description: body.Description,
		Status:      RequestStatusPending,
		CreatedAt:   time.Now(),
	}

	if err := dbSaveChannelRequest(ctx, req); err != nil {
		http.Error(w, "error saving request", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "id": req.ID})
}

// GET /api/super-admin/channel-requests
func listChannelRequests(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	requests, err := dbListChannelRequests(ctx)
	if err != nil {
		requests = []*ChannelRequest{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(requests)
}

// POST /api/super-admin/channel-requests/{id}/approve
func approveChannelRequest(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	id := chi.URLParam(r, "id")

	var body struct {
		Slug  string `json:"slug"`
		Notes string `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	req, err := dbGetChannelRequest(ctx, id)
	if err != nil {
		http.Error(w, "request not found", http.StatusNotFound)
		return
	}

	finalSlug := body.Slug
	if finalSlug == "" {
		finalSlug = req.DesiredSlug
	}

	if !slugRegex.MatchString(finalSlug) {
		http.Error(w, "invalid slug format", http.StatusBadRequest)
		return
	}

	exists, err := dbChannelExists(ctx, finalSlug)
	if err != nil {
		http.Error(w, "error checking slug", http.StatusInternalServerError)
		return
	}
	if exists {
		http.Error(w, "slug already taken", http.StatusConflict)
		return
	}

	channel := &ChannelData{
		Slug:       finalSlug,
		Name:       req.Name,
		OwnerEmail: req.Email,
		CreatedAt:  time.Now(),
		Features: ChannelFeatures{
			Reactions:         true,
			FileUploads:       true,
			Reports:           true,
			ScheduledMessages: true,
		},
	}

	if err := dbCreateChannel(ctx, channel); err != nil {
		http.Error(w, "error creating channel", http.StatusInternalServerError)
		return
	}

	dbAssignChannelRole(ctx, req.Email, finalSlug, RoleOwner)
	initializePrivilegeUsers()

	req.Status = RequestStatusApproved
	req.ApprovedSlug = finalSlug
	req.Notes = body.Notes
	dbSaveChannelRequest(ctx, req)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":      "approved",
		"channelSlug": finalSlug,
		"channelName": channel.Name,
		"ownerEmail":  req.Email,
	})
}

// POST /api/super-admin/channel-requests/{id}/reject
func rejectChannelRequest(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	id := chi.URLParam(r, "id")

	var body struct {
		Notes string `json:"notes"`
	}
	json.NewDecoder(r.Body).Decode(&body)

	req, err := dbGetChannelRequest(ctx, id)
	if err != nil {
		http.Error(w, "request not found", http.StatusNotFound)
		return
	}

	req.Status = RequestStatusRejected
	req.Notes = body.Notes
	dbSaveChannelRequest(ctx, req)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "rejected"})
}
