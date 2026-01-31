// Package raft raft/rpc.go
package raft

// RequestVoteArgs RequestVote RPC (Figure 2 in the paper)
type RequestVoteArgs struct {
	Term         int    // candidate's term
	CandidateId  string // candidate requesting vote
	LastLogIndex int    // index of candidate's last log entry
	LastLogTerm  int    // term of candidate's last log entry
}

type RequestVoteReply struct {
	Term        int  // currentTerm, for a candidate to update itself
	VoteGranted bool // true means a candidate received a vote
}

// AppendEntriesArgs AppendEntries RPC (Figure 2 in the paper)
type AppendEntriesArgs struct {
	Term         int        // leader's term
	LeaderId     string     // so followers can redirect clients
	PrevLogIndex int        // index of log entry immediately preceding new ones
	PrevLogTerm  int        // term of prevLogIndex entry
	Entries      []LogEntry // log entries to store (empty for heartbeat)
	LeaderCommit int        // leader's commitIndex
}

type AppendEntriesReply struct {
	Term    int  // currentTerm, for leader to update itself
	Success bool // true if the follower contained entry matching prevLogIndex and prevLogTerm
}
