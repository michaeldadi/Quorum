// Package kv internal/kv/http.go
package kv

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"quorum/internal/raft"
	"quorum/pkg/logger"
	"time"
)

type HTTPServer struct {
	store    *Store
	node     *raft.Node
	nodeID   string
	httpAddr string
	peerHTTP map[string]string
}

type HTTPConfig struct {
	Store    *Store
	Node     *raft.Node
	NodeID   string
	Addr     string
	PeerHTTP map[string]string
}

func NewHTTPServer(cfg HTTPConfig) *HTTPServer {
	s := &HTTPServer{
		store:    cfg.Store,
		node:     cfg.Node,
		nodeID:   cfg.NodeID,
		httpAddr: cfg.Addr,
		peerHTTP: cfg.PeerHTTP,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/get", s.handleGet)
	mux.HandleFunc("/put", s.handlePut)
	mux.HandleFunc("/delete", s.handleDelete)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/cluster/add", s.handleAddServer)
	mux.HandleFunc("/cluster/remove", s.handleRemoveServer)
	mux.HandleFunc("/cluster/members", s.handleMembers)

	go func() {
		logger.Info("HTTP server listening", "addr", cfg.Addr)
		srv := &http.Server{
			Addr:              cfg.Addr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
		}
		if err := srv.ListenAndServe(); err != nil {
			logger.Error("HTTP server error", "err", err)
		}
	}()

	return s
}

type Response struct {
	Ok       bool   `json:"ok"`
	Value    string `json:"value,omitempty"`
	Error    string `json:"error,omitempty"`
	Leader   string `json:"leader,omitempty"`
	Redirect string `json:"redirect,omitempty"`
}

func (s *HTTPServer) handleGet(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		s.jsonResponse(w, http.StatusBadRequest, Response{Error: "missing key"})
		return
	}

	local := r.URL.Query().Get("local") == "true"

	var value string
	var ok bool
	var err error

	if local {
		value, ok = s.store.GetLocal(key)
	} else {
		value, ok, err = s.store.Get(key)
		if errors.Is(err, ErrNotLeader) {
			s.redirectToLeader(w, r)
			return
		}
		if err != nil {
			s.jsonResponse(w, http.StatusInternalServerError, Response{Error: err.Error()})
			return
		}
	}

	if !ok {
		s.jsonResponse(w, http.StatusNotFound, Response{Error: "key not found"})
		return
	}

	s.jsonResponse(w, http.StatusOK, Response{Ok: true, Value: value})
}

func (s *HTTPServer) handlePut(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.jsonResponse(w, http.StatusMethodNotAllowed, Response{Error: "use POST"})
		return
	}

	var req struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonResponse(w, http.StatusBadRequest, Response{Error: "invalid JSON"})
		return
	}

	if req.Key == "" {
		s.jsonResponse(w, http.StatusBadRequest, Response{Error: "missing key"})
		return
	}

	err := s.store.Put(req.Key, req.Value)
	if errors.Is(err, ErrNotLeader) {
		s.redirectToLeader(w, r)
		return
	}
	if err != nil {
		s.jsonResponse(w, http.StatusInternalServerError, Response{Error: err.Error()})
		return
	}

	s.jsonResponse(w, http.StatusOK, Response{Ok: true})
}

func (s *HTTPServer) handleDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.jsonResponse(w, http.StatusMethodNotAllowed, Response{Error: "use POST"})
		return
	}

	key := r.URL.Query().Get("key")
	if key == "" {
		s.jsonResponse(w, http.StatusBadRequest, Response{Error: "missing key"})
		return
	}

	err := s.store.Delete(key)
	if errors.Is(err, ErrNotLeader) {
		s.redirectToLeader(w, r)
		return
	}
	if err != nil {
		s.jsonResponse(w, http.StatusInternalServerError, Response{Error: err.Error()})
		return
	}

	s.jsonResponse(w, http.StatusOK, Response{Ok: true})
}

func (s *HTTPServer) handleHealth(w http.ResponseWriter, _ *http.Request) {
	s.jsonResponse(w, http.StatusOK, Response{Ok: true})
}

func (s *HTTPServer) handleStatus(w http.ResponseWriter, _ *http.Request) {
	leader := s.store.GetLeader()
	isLeader := s.store.IsLeader()
	term, state := s.node.GetState()
	peers := s.node.GetPeers()

	w.Header().Set("Content-Type", "application/json")
	err := json.NewEncoder(w).Encode(map[string]interface{}{
		"nodeId":   s.nodeID,
		"isLeader": isLeader,
		"leader":   leader,
		"term":     term,
		"state":    state.String(),
		"peers":    peers,
	})
	if err != nil {
		return
	}
}

func (s *HTTPServer) handleAddServer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.jsonResponse(w, http.StatusMethodNotAllowed, Response{Error: "use POST"})
		return
	}

	var req struct {
		NodeID   string `json:"nodeId"`
		RaftAddr string `json:"raftAddr"`
		HTTPAddr string `json:"httpAddr"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonResponse(w, http.StatusBadRequest, Response{Error: "invalid JSON"})
		return
	}

	if req.NodeID == "" || req.RaftAddr == "" {
		s.jsonResponse(w, http.StatusBadRequest, Response{Error: "nodeId and raftAddr required"})
		return
	}

	index, _, err := s.node.AddServer(req.NodeID, req.RaftAddr)
	if errors.Is(err, raft.ErrNotLeader) {
		s.redirectToLeader(w, r)
		return
	}
	if err != nil {
		s.jsonResponse(w, http.StatusInternalServerError, Response{Error: err.Error()})
		return
	}

	// Update HTTP peer mapping if provided
	if req.HTTPAddr != "" {
		s.peerHTTP[req.NodeID] = req.HTTPAddr
	}

	w.Header().Set("Content-Type", "application/json")
	err = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":    true,
		"index": index,
	})
	if err != nil {
		return
	}
}

func (s *HTTPServer) handleRemoveServer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.jsonResponse(w, http.StatusMethodNotAllowed, Response{Error: "use POST"})
		return
	}

	var req struct {
		NodeID string `json:"nodeId"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonResponse(w, http.StatusBadRequest, Response{Error: "invalid JSON"})
		return
	}

	if req.NodeID == "" {
		s.jsonResponse(w, http.StatusBadRequest, Response{Error: "nodeId required"})
		return
	}

	index, _, err := s.node.RemoveServer(req.NodeID)
	if errors.Is(err, raft.ErrNotLeader) {
		s.redirectToLeader(w, r)
		return
	}
	if err != nil {
		s.jsonResponse(w, http.StatusInternalServerError, Response{Error: err.Error()})
		return
	}

	delete(s.peerHTTP, req.NodeID)

	w.Header().Set("Content-Type", "application/json")
	err = json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":    true,
		"index": index,
	})
	if err != nil {
		return
	}
}

func (s *HTTPServer) handleMembers(w http.ResponseWriter, _ *http.Request) {
	peers := s.node.GetPeers()

	w.Header().Set("Content-Type", "application/json")
	err := json.NewEncoder(w).Encode(map[string]interface{}{
		"members": append([]string{s.nodeID}, peers...),
	})
	if err != nil {
		return
	}
}

func (s *HTTPServer) redirectToLeader(w http.ResponseWriter, r *http.Request) {
	leader := s.store.GetLeader()
	if leader == "" || leader == s.nodeID {
		s.jsonResponse(w, http.StatusServiceUnavailable, Response{
			Error: "no leader elected",
		})
		return
	}

	leaderHTTP, ok := s.peerHTTP[leader]
	if !ok {
		s.jsonResponse(w, http.StatusServiceUnavailable, Response{
			Error:  "leader unknown",
			Leader: leader,
		})
		return
	}

	redirectURL := fmt.Sprintf("http://%s%s", leaderHTTP, r.URL.Path)
	if r.URL.RawQuery != "" {
		redirectURL += "?" + r.URL.RawQuery
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Location", redirectURL)
	w.WriteHeader(http.StatusTemporaryRedirect)

	err := json.NewEncoder(w).Encode(Response{
		Error:    "not leader",
		Leader:   leader,
		Redirect: redirectURL,
	})
	if err != nil {
		return
	}
}

func (s *HTTPServer) jsonResponse(w http.ResponseWriter, status int, resp Response) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	err := json.NewEncoder(w).Encode(resp)
	if err != nil {
		return
	}
}
