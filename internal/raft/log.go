// Package raft raft/log.go
package raft

type LogEntry struct {
	Term    int         // term when leader received entry
	Index   int         // position in the log
	Command interface{} // state machine command
}
