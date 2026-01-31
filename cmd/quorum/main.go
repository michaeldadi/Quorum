// main.go
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"quorum/internal/kv"
	"quorum/internal/raft"
	"quorum/pkg/logger"
	"strings"
	"syscall"
	"time"
)

func main() {
	nodeID := flag.String("id", "node-1", "node ID")
	port := flag.Int("port", 9001, "RPC port")
	httpPort := flag.Int("http", 8001, "HTTP port")
	dataDir := flag.String("data", "./data", "data directory")
	peersFlag := flag.String("peers", "", "comma-separated list of peer RPC addresses")
	peerHTTPFlag := flag.String("peer-http", "", "comma-separated list of nodeId=httpAddr mappings")
	flag.Parse()

	logger.Init(*nodeID)

	var peers []string
	peerHTTP := make(map[string]string)

	if *peersFlag != "" {
		// Use provided peers
		peers = strings.Split(*peersFlag, ",")
	} else {
		// Default cluster config (for local development)
		allNodes := map[string]struct {
			rpc  int
			http int
		}{
			"node-1": {9001, 8001},
			"node-2": {9002, 8002},
			"node-3": {9003, 8003},
		}

		for id, ports := range allNodes {
			peerHTTP[id] = fmt.Sprintf("localhost:%d", ports.http)
			if id != *nodeID {
				peers = append(peers, fmt.Sprintf("localhost:%d", ports.rpc))
			}
		}
	}

	// Parse peer HTTP mappings
	if *peerHTTPFlag != "" {
		for _, mapping := range strings.Split(*peerHTTPFlag, ",") {
			parts := strings.SplitN(mapping, "=", 2)
			if len(parts) == 2 {
				peerHTTP[parts[0]] = parts[1]
			}
		}
	}

	nodeDataDir := filepath.Join(*dataDir, *nodeID)
	persister, err := raft.NewPersister(nodeDataDir)
	if err != nil {
		logger.Error("failed to create persister", "err", err)
		os.Exit(1)
	}

	applyCh := make(chan raft.ApplyMsg)
	node := raft.NewNode(*nodeID, peers, applyCh, persister)

	_, err = raft.NewRPCServer(node, *port)
	if err != nil {
		logger.Error("failed to start RPC server", "err", err)
		os.Exit(1)
	}

	node.Start()

	store := kv.NewStore(node, applyCh)

	kv.NewHTTPServer(kv.HTTPConfig{
		Store:    store,
		Node:     node,
		NodeID:   *nodeID,
		Addr:     fmt.Sprintf(":%d", *httpPort),
		PeerHTTP: peerHTTP,
	})

	go func() {
		for {
			time.Sleep(5 * time.Second)
			term, state := node.GetState()
			logger.Info("status", "term", term, "state", state, "leader", node.GetLeader(), "peers", node.GetPeers())
		}
	}()

	logger.Info("node ready",
		"id", *nodeID,
		"rpc", *port,
		"http", *httpPort,
		"data", nodeDataDir,
		"peers", peers)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	node.Stop()
	logger.Info("shutdown complete")
}
