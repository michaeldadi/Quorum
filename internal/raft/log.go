// Package raft raft/log.go
package raft

type EntryType int

const (
	EntryCommand EntryType = iota
	EntryConfig
)

type LogEntry struct {
	Term    int
	Index   int
	Type    EntryType
	Command interface{}
}

type ApplyMsg struct {
	CommandValid bool
	Command      interface{}
	CommandIndex int

	// For snapshots
	SnapshotValid bool
	Snapshot      []byte
	SnapshotTerm  int
	SnapshotIndex int

	// For config changes
	ConfigValid  bool
	ConfigChange ConfigChange
	ConfigIndex  int
}
