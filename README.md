# quorum

Distributed key-value store with Raft consensus implemented from scratch in Go.

This is a learning project—no external consensus libraries, just the [Raft paper](https://raft.github.io/raft.pdf) and
Go's standard library.

## Features

- [ ] Leader election with randomized timeouts
- [ ] Log replication across nodes
- [ ] Safety guarantees (election safety, log matching, leader completeness)
- [ ] Persistence and crash recovery
- [ ] Membership changes
- [ ] Key-value store interface (`Get`, `Put`, `Delete`)
- [ ] HTTP API for client interaction
- [ ] Chaos testing utilities

## Getting Started

```bash
# Build
make build

# Run tests
make test

# Lint
make lint

# Start a 3-node local cluster
make cluster
```

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                      Client                             │
└─────────────────────┬───────────────────────────────────┘
                      │ HTTP
┌─────────────────────▼───────────────────────────────────┐
│                   KV Store                              │
│                  (Get/Put/Delete)                       │
└─────────────────────┬───────────────────────────────────┘
                      │
┌─────────────────────▼───────────────────────────────────┐
│                  Raft Layer                             │
│  ┌─────────┐    ┌─────────┐    ┌─────────┐             │
│  │ Node 1  │◄──►│ Node 2  │◄──►│ Node 3  │             │
│  │(Leader) │    │(Follower)│   │(Follower)│             │
│  └─────────┘    └─────────┘    └─────────┘             │
└─────────────────────────────────────────────────────────┘
```

## Project Structure

```
quorum/
├── cmd/quorum/       # Entry point
├── internal/raft/    # Raft consensus implementation
├── pkg/logger/       # Structured logging
└── scripts/          # Dev tooling
```

## Raft Implementation Notes

Following Figure 2 from the Raft paper:

**State on all servers:**

- `currentTerm` — latest term server has seen
- `votedFor` — candidateId that received vote in current term
- `log[]` — log entries

**State on leaders:**

- `nextIndex[]` — next log entry to send to each peer
- `matchIndex[]` — highest log entry known to be replicated on each peer

**RPCs:**

- `RequestVote` — used by candidates to gather votes
- `AppendEntries` — used by leader to replicate log and send heartbeats

## Resources

- [Raft Paper](https://raft.github.io/raft.pdf)
- [Raft Visualization](https://thesecretlivesofdata.com/raft/)
- [MIT 6.824 Labs](https://pdos.csail.mit.edu/6.824/)

## License

MIT
