// Package raft raft/node.go
package raft

import (
	"sync"
)

type Node struct {
	mu sync.Mutex

	// Persistent state (survives restarts)
	id          string
	currentTerm int
	votedFor    string // candidateId that received vote in current term
	log         []LogEntry

	// Volatile state
	state       NodeState
	commitIndex int // highest log entry known to be committed
	lastApplied int // highest log entry applied to state machine

	// Leader-only volatile state
	nextIndex  map[string]int // for each peer: next log index to send
	matchIndex map[string]int // for each peer: highest log index known to be replicated

	// Channels for coordination
	heartbeat     chan struct{}
	electionReset chan struct{}

	// Cluster info
	peers []string
}

func NewNode(id string, peers []string) *Node {
	return &Node{
		id:            id,
		currentTerm:   0,
		votedFor:      "",
		log:           make([]LogEntry, 0),
		state:         Follower,
		commitIndex:   0,
		lastApplied:   0,
		nextIndex:     make(map[string]int),
		matchIndex:    make(map[string]int),
		heartbeat:     make(chan struct{}),
		electionReset: make(chan struct{}),
		peers:         peers,
	}
}

func (n *Node) GetState() (int, NodeState) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.currentTerm, n.state
}
