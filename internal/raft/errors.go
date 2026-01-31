// Package raft internal/raft/errors.go
package raft

import "errors"

var (
	ErrNotLeader        = errors.New("not leader")
	ErrConfigInProgress = errors.New("config change already in progress")
	ErrAlreadyMember    = errors.New("server already in cluster")
)
