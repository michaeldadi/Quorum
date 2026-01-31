// Package raft raft/node.go
package raft

import (
	"crypto/rand"
	"encoding/json"
	"math/big"
	"quorum/pkg/logger"
	"sync"
	"time"
)

const (
	minElectionTimeout = 150 * time.Millisecond
	maxElectionTimeout = 300 * time.Millisecond
)

type Node struct {
	mu sync.Mutex

	// Persistent state
	id          string
	currentTerm int
	votedFor    string
	log         []LogEntry

	// Snapshot state
	lastIncludedIndex int
	lastIncludedTerm  int
	snapshot          []byte

	// Volatile state
	state       NodeState
	commitIndex int
	lastApplied int
	leaderId    string

	// Leader state
	nextIndex  map[string]int
	matchIndex map[string]int

	// Cluster membership
	peers         []string          // RPC addresses
	peerIds       map[string]string // addr -> nodeId
	configPending bool              // true if config change in progress

	// Channels
	resetElection chan struct{}
	stopCh        chan struct{}
	applyCh       chan ApplyMsg

	// Persistence
	persister *Persister

	// For linearizable reads
	readIndexCh chan readIndexRequest
}

type readIndexRequest struct {
}

func NewNode(id string, peers []string, applyCh chan ApplyMsg, persister *Persister) *Node {
	n := &Node{
		id:                id,
		currentTerm:       0,
		votedFor:          "",
		log:               make([]LogEntry, 0),
		lastIncludedIndex: 0,
		lastIncludedTerm:  0,
		state:             Follower,
		commitIndex:       0,
		lastApplied:       0,
		leaderId:          "",
		nextIndex:         make(map[string]int),
		matchIndex:        make(map[string]int),
		peers:             peers,
		peerIds:           make(map[string]string),
		configPending:     false,
		resetElection:     make(chan struct{}),
		stopCh:            make(chan struct{}),
		applyCh:           applyCh,
		persister:         persister,
		readIndexCh:       make(chan readIndexRequest),
	}

	if persister != nil {
		state, err := persister.Load()
		if err != nil {
			logger.Error("failed to load persisted state", "err", err)
		} else {
			n.currentTerm = state.CurrentTerm
			n.votedFor = state.VotedFor
			n.log = state.Log
			n.lastIncludedIndex = state.LastIncludedIndex
			n.lastIncludedTerm = state.LastIncludedTerm
			if state.Peers != nil {
				n.peers = state.Peers
			}
		}

		snapshot, err := persister.LoadSnapshot()
		if err != nil {
			logger.Error("failed to load snapshot", "err", err)
		} else {
			n.snapshot = snapshot
		}
	}

	return n
}

func (n *Node) Start() {
	logger.Info("node starting",
		"id", n.id,
		"peers", n.peers,
		"term", n.currentTerm,
		"logLen", len(n.log),
		"snapshotIndex", n.lastIncludedIndex)

	if len(n.snapshot) > 0 {
		n.applyCh <- ApplyMsg{
			SnapshotValid: true,
			Snapshot:      n.snapshot,
			SnapshotTerm:  n.lastIncludedTerm,
			SnapshotIndex: n.lastIncludedIndex,
		}
		n.lastApplied = n.lastIncludedIndex
		n.commitIndex = n.lastIncludedIndex
	}

	go n.electionLoop()
	go n.applyLoop()
}

func (n *Node) Stop() {
	close(n.stopCh)
}

func (n *Node) persist() {
	if n.persister == nil {
		return
	}

	state := PersistedState{
		CurrentTerm:       n.currentTerm,
		VotedFor:          n.votedFor,
		Log:               n.log,
		LastIncludedIndex: n.lastIncludedIndex,
		LastIncludedTerm:  n.lastIncludedTerm,
		Peers:             n.peers,
	}

	if err := n.persister.Save(&state); err != nil {
		logger.Error("failed to persist state", "err", err)
	}
}

// AddServer adds a new server to the cluster
func (n *Node) AddServer(nodeID, addr string) (index, term int, err error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.state != Leader {
		return -1, -1, ErrNotLeader
	}

	if n.configPending {
		return -1, -1, ErrConfigInProgress
	}

	// Check if already in cluster
	for _, peer := range n.peers {
		if peer == addr {
			return -1, -1, ErrAlreadyMember
		}
	}

	change := ConfigChange{
		Type:   AddNode,
		NodeID: nodeID,
		Addr:   addr,
	}

	changeBytes, _ := json.Marshal(change)

	index = n.lastIncludedIndex + len(n.log) + 1
	term = n.currentTerm

	entry := LogEntry{
		Term:    term,
		Index:   index,
		Type:    EntryConfig,
		Command: string(changeBytes),
	}

	n.log = append(n.log, entry)
	n.configPending = true
	n.matchIndex[n.id] = index
	n.persist()

	logger.Info("leader accepted config change",
		"type", "AddNode",
		"nodeId", nodeID,
		"addr", addr,
		"index", index)

	return index, term, nil
}

// RemoveServer removes a server from the cluster
func (n *Node) RemoveServer(nodeID string) (index, term int, err error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.state != Leader {
		return -1, -1, ErrNotLeader
	}

	if n.configPending {
		return -1, -1, ErrConfigInProgress
	}

	change := ConfigChange{
		Type:   RemoveNode,
		NodeID: nodeID,
	}

	changeBytes, _ := json.Marshal(change)

	index = n.lastIncludedIndex + len(n.log) + 1
	term = n.currentTerm

	entry := LogEntry{
		Term:    term,
		Index:   index,
		Type:    EntryConfig,
		Command: string(changeBytes),
	}

	n.log = append(n.log, entry)
	n.configPending = true
	n.matchIndex[n.id] = index
	n.persist()

	logger.Info("leader accepted config change",
		"type", "RemoveNode",
		"nodeId", nodeID,
		"index", index)

	return index, term, nil
}

// ApplyConfigChange applies a committed config change
func (n *Node) applyConfigChange(change ConfigChange) {
	n.mu.Lock()
	defer n.mu.Unlock()

	switch change.Type {
	case AddNode:
		// Add to peers if not already present
		for _, peer := range n.peers {
			if peer == change.Addr {
				n.configPending = false
				return
			}
		}
		n.peers = append(n.peers, change.Addr)
		n.peerIds[change.Addr] = change.NodeID

		// Initialize leader state for new peer
		if n.state == Leader {
			n.nextIndex[change.Addr] = n.lastIncludedIndex + len(n.log) + 1
			n.matchIndex[change.Addr] = 0
		}

		logger.Info("added server to cluster", "nodeId", change.NodeID, "addr", change.Addr, "peers", n.peers)

	case RemoveNode:
		// Find and remove the peer
		var addr string
		for a, id := range n.peerIds {
			if id == change.NodeID {
				addr = a
				break
			}
		}

		// Also check if it's us being removed
		if change.NodeID == n.id {
			logger.Warn("this node has been removed from cluster")
			// Could trigger shutdown here
		}

		if addr != "" {
			newPeers := make([]string, 0, len(n.peers)-1)
			for _, peer := range n.peers {
				if peer != addr {
					newPeers = append(newPeers, peer)
				}
			}
			n.peers = newPeers
			delete(n.peerIds, addr)
			delete(n.nextIndex, addr)
			delete(n.matchIndex, addr)
		}

		logger.Info("removed server from cluster", "nodeId", change.NodeID, "peers", n.peers)
	}

	n.configPending = false
	n.persist()
}

func (n *Node) GetPeers() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	result := make([]string, len(n.peers))
	copy(result, n.peers)
	return result
}

func (n *Node) electionLoop() {
	for {
		timeout := randomElectionTimeout()

		select {
		case <-time.After(timeout):
			n.mu.Lock()
			state := n.state
			n.mu.Unlock()

			if state != Leader {
				n.startElection()
			}

		case <-n.resetElection:

		case <-n.stopCh:
			logger.Info("election loop stopping")
			return
		}
	}
}

func (n *Node) applyLoop() {
	for {
		select {
		case <-n.stopCh:
			return
		default:
		}

		n.mu.Lock()
		for n.commitIndex > n.lastApplied {
			n.lastApplied++

			logIndex := n.lastApplied - n.lastIncludedIndex - 1
			if logIndex < 0 || logIndex >= len(n.log) {
				n.mu.Unlock()
				time.Sleep(10 * time.Millisecond)
				n.mu.Lock()
				continue
			}

			entry := n.log[logIndex]

			if entry.Type == EntryConfig {
				// Parse and apply config change
				var change ConfigChange
				if err := json.Unmarshal([]byte(entry.Command.(string)), &change); err != nil {
					logger.Error("failed to unmarshal config change", "err", err)
				} else {
					n.mu.Unlock()
					n.applyConfigChange(change)
					n.applyCh <- ApplyMsg{
						ConfigValid:  true,
						ConfigChange: change,
						ConfigIndex:  entry.Index,
					}
					n.mu.Lock()
				}
			} else {
				msg := ApplyMsg{
					CommandValid: true,
					Command:      entry.Command,
					CommandIndex: entry.Index,
				}

				n.mu.Unlock()
				n.applyCh <- msg
				n.mu.Lock()

				logger.Debug("applied entry", "index", entry.Index, "term", entry.Term)
			}
		}
		n.mu.Unlock()

		time.Sleep(10 * time.Millisecond)
	}
}

func (n *Node) Submit(command interface{}) (index, term int, isLeader bool) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.state != Leader {
		return -1, -1, false
	}

	index = n.lastIncludedIndex + len(n.log) + 1
	term = n.currentTerm

	entry := LogEntry{
		Term:    term,
		Index:   index,
		Type:    EntryCommand,
		Command: command,
	}

	n.log = append(n.log, entry)
	n.matchIndex[n.id] = index
	n.persist()

	logger.Info("leader accepted command", "index", index, "term", term)

	return index, term, true
}

func (n *Node) becomeLeader() {
	n.state = Leader
	n.leaderId = n.id
	logger.Info("became leader", "term", n.currentTerm)

	lastLogIndex := n.lastIncludedIndex + len(n.log)
	for _, peer := range n.peers {
		n.nextIndex[peer] = lastLogIndex + 1
		n.matchIndex[peer] = 0
	}
	n.matchIndex[n.id] = lastLogIndex

	go n.heartbeatLoop()
}

func (n *Node) becomeFollower(term int) {
	n.state = Follower
	n.currentTerm = term
	n.votedFor = ""
	n.persist()
	logger.Info("became follower", "term", n.currentTerm)
}

func (n *Node) ResetElectionTimer() {
	select {
	case n.resetElection <- struct{}{}:
	default:
	}
}

func (n *Node) GetState() (int, NodeState) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.currentTerm, n.state
}

func (n *Node) GetLeader() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.leaderId
}

func (n *Node) IsLeader() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.state == Leader
}

func (n *Node) lastLogInfo() (index, term int) {
	if len(n.log) == 0 {
		return n.lastIncludedIndex, n.lastIncludedTerm
	}
	last := n.log[len(n.log)-1]
	return last.Index, last.Term
}

func (n *Node) logTerm(index int) int {
	if index == n.lastIncludedIndex {
		return n.lastIncludedTerm
	}
	logIndex := index - n.lastIncludedIndex - 1
	if logIndex < 0 || logIndex >= len(n.log) {
		return -1
	}
	return n.log[logIndex].Term
}

func (n *Node) Snapshot(index int, data []byte) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if index <= n.lastIncludedIndex {
		return
	}

	logIndex := index - n.lastIncludedIndex - 1
	if logIndex < 0 || logIndex >= len(n.log) {
		logger.Error("snapshot index out of range", "index", index)
		return
	}

	n.lastIncludedTerm = n.log[logIndex].Term
	n.lastIncludedIndex = index
	n.snapshot = data

	n.log = n.log[logIndex+1:]

	n.persist()
	if n.persister != nil {
		if err := n.persister.SaveSnapshot(data); err != nil {
			logger.Error("failed to save snapshot", "err", err)
		}
	}

	logger.Info("created snapshot",
		"index", index,
		"term", n.lastIncludedTerm,
		"remainingLog", len(n.log))
}

func (n *Node) ReadIndex() (int, bool) {
	n.mu.Lock()
	if n.state != Leader {
		n.mu.Unlock()
		return 0, false
	}

	if len(n.peers) == 0 {
		index := n.commitIndex
		n.mu.Unlock()
		return index, true
	}

	currentTerm := n.currentTerm
	commitIndex := n.commitIndex
	n.mu.Unlock()

	confirmed := n.confirmLeadership(currentTerm)
	if !confirmed {
		return 0, false
	}

	return commitIndex, true
}

func (n *Node) confirmLeadership(term int) bool {
	responses := make(chan bool, len(n.peers))

	n.mu.Lock()
	if n.state != Leader || n.currentTerm != term {
		n.mu.Unlock()
		return false
	}

	args := &AppendEntriesArgs{
		Term:         n.currentTerm,
		LeaderId:     n.id,
		PrevLogIndex: n.lastIncludedIndex + len(n.log),
		PrevLogTerm:  n.logTerm(n.lastIncludedIndex + len(n.log)),
		Entries:      nil,
		LeaderCommit: n.commitIndex,
	}
	peers := n.peers
	n.mu.Unlock()

	for _, peer := range peers {
		go func(peer string) {
			reply, ok := n.sendAppendEntries(peer, args)
			if ok && reply.Success {
				responses <- true
			} else {
				responses <- false
			}
		}(peer)
	}

	needed := (len(peers)+1)/2 + 1
	confirmed := 1
	failed := 0
	timeout := time.After(100 * time.Millisecond)

	for confirmed < needed && failed <= len(peers)/2 {
		select {
		case success := <-responses:
			if success {
				confirmed++
			} else {
				failed++
			}
		case <-timeout:
			return false
		}
	}

	n.mu.Lock()
	stillLeader := n.state == Leader && n.currentTerm == term
	n.mu.Unlock()

	return stillLeader && confirmed >= needed
}

func (n *Node) WaitForApply(index int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		n.mu.Lock()
		if n.lastApplied >= index {
			n.mu.Unlock()
			return true
		}
		n.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func randomElectionTimeout() time.Duration {
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(maxElectionTimeout-minElectionTimeout)))
	return minElectionTimeout + time.Duration(n.Int64())
}
