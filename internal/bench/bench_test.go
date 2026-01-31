package bench

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"quorum/internal/kv"
	"quorum/internal/raft"
)

type benchCluster struct {
	nodes    []*raft.Node
	servers  []*raft.RPCServer
	stores   []*kv.Store
	applyChs []chan raft.ApplyMsg
	dataDir  string
}

func newBenchCluster(b *testing.B, n int) *benchCluster {
	dataDir, _ := os.MkdirTemp("", "bench-*")

	bc := &benchCluster{
		nodes:    make([]*raft.Node, n),
		servers:  make([]*raft.RPCServer, n),
		stores:   make([]*kv.Store, n),
		applyChs: make([]chan raft.ApplyMsg, n),
		dataDir:  dataDir,
	}

	basePort := 16000

	peers := make([][]string, n)
	for i := 0; i < n; i++ {
		peers[i] = make([]string, 0, n-1)
		for j := 0; j < n; j++ {
			if i != j {
				peers[i] = append(peers[i], fmt.Sprintf("localhost:%d", basePort+j))
			}
		}
	}

	for i := 0; i < n; i++ {
		nodeID := fmt.Sprintf("node-%d", i)
		nodeDir := fmt.Sprintf("%s/%s", dataDir, nodeID)

		persister, _ := raft.NewPersister(nodeDir)
		bc.applyChs[i] = make(chan raft.ApplyMsg, 10000)
		bc.nodes[i] = raft.NewNode(nodeID, peers[i], bc.applyChs[i], persister)

		bc.servers[i], _ = raft.NewRPCServer(bc.nodes[i], basePort+i)
	}

	for i, node := range bc.nodes {
		node.Start()
		bc.stores[i] = kv.NewStore(node, bc.applyChs[i])
	}

	return bc
}

func (bc *benchCluster) cleanup() {
	for _, node := range bc.nodes {
		if node != nil {
			node.Stop()
		}
	}
	for _, server := range bc.servers {
		if server != nil {
			err := server.Close()
			if err != nil {
				return
			}
		}
	}
	err := os.RemoveAll(bc.dataDir)
	if err != nil {
		return
	}
}

func (bc *benchCluster) waitForLeader(timeout time.Duration) *kv.Store {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, store := range bc.stores {
			if store != nil && store.IsLeader() {
				return store
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil
}

// --- Benchmarks ---

func BenchmarkPut_SingleClient(b *testing.B) {
	bc := newBenchCluster(b, 3)
	defer bc.cleanup()

	leader := bc.waitForLeader(5 * time.Second)
	if leader == nil {
		b.Fatal("no leader")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key-%d", i)
		value := fmt.Sprintf("value-%d", i)
		if err := leader.Put(key, value); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()

	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "ops/sec")
}

func BenchmarkPut_Concurrent(b *testing.B) {
	bc := newBenchCluster(b, 3)
	defer bc.cleanup()

	leader := bc.waitForLeader(5 * time.Second)
	if leader == nil {
		b.Fatal("no leader")
	}

	b.ResetTimer()

	var counter atomic.Int64
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			i := counter.Add(1)
			key := fmt.Sprintf("key-%d", i)
			value := fmt.Sprintf("value-%d", i)
			_ = leader.Put(key, value)
		}
	})
	b.StopTimer()

	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "ops/sec")
}

func BenchmarkGet_Linearizable(b *testing.B) {
	bc := newBenchCluster(b, 3)
	defer bc.cleanup()

	leader := bc.waitForLeader(5 * time.Second)
	if leader == nil {
		b.Fatal("no leader")
	}

	// Pre-populate
	for i := 0; i < 1000; i++ {
		err := leader.Put(fmt.Sprintf("key-%d", i), fmt.Sprintf("value-%d", i))
		if err != nil {
			return
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key-%d", i%1000)
		_, _, err := leader.Get(key)
		if err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()

	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "ops/sec")
}

func BenchmarkGet_Local(b *testing.B) {
	bc := newBenchCluster(b, 3)
	defer bc.cleanup()

	leader := bc.waitForLeader(5 * time.Second)
	if leader == nil {
		b.Fatal("no leader")
	}

	// Pre-populate
	for i := 0; i < 1000; i++ {
		err := leader.Put(fmt.Sprintf("key-%d", i), fmt.Sprintf("value-%d", i))
		if err != nil {
			return
		}
	}

	time.Sleep(500 * time.Millisecond) // Let replication happen

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := fmt.Sprintf("key-%d", i%1000)
		leader.GetLocal(key)
	}
	b.StopTimer()

	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "ops/sec")
}

func BenchmarkMixedWorkload(b *testing.B) {
	bc := newBenchCluster(b, 3)
	defer bc.cleanup()

	leader := bc.waitForLeader(5 * time.Second)
	if leader == nil {
		b.Fatal("no leader")
	}

	// Pre-populate
	for i := 0; i < 100; i++ {
		err := leader.Put(fmt.Sprintf("key-%d", i), fmt.Sprintf("value-%d", i))
		if err != nil {
			return
		}
	}

	b.ResetTimer()

	var counter atomic.Int64
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			i := counter.Add(1)
			if i%10 < 8 { // 80% reads
				key := fmt.Sprintf("key-%d", i%100)
				leader.GetLocal(key)
			} else { // 20% writes
				key := fmt.Sprintf("key-%d", i%100)
				err := leader.Put(key, fmt.Sprintf("updated-%d", i))
				if err != nil {
					return
				}
			}
		}
	})
	b.StopTimer()

	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "ops/sec")
}

// Latency measurement
func BenchmarkPutLatency(b *testing.B) {
	bc := newBenchCluster(b, 3)
	defer bc.cleanup()

	leader := bc.waitForLeader(5 * time.Second)
	if leader == nil {
		b.Fatal("no leader")
	}

	var totalLatency time.Duration
	var count int

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := time.Now()
		key := fmt.Sprintf("key-%d", i)
		value := fmt.Sprintf("value-%d", i)
		if err := leader.Put(key, value); err == nil {
			totalLatency += time.Since(start)
			count++
		}
	}
	b.StopTimer()

	if count > 0 {
		avgLatency := totalLatency / time.Duration(count)
		b.ReportMetric(float64(avgLatency.Microseconds()), "avg_latency_µs")
	}
}

// Throughput over time
func TestThroughputOverTime(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping throughput test in short mode")
	}

	bc := newBenchCluster(&testing.B{}, 3)
	defer bc.cleanup()

	leader := bc.waitForLeader(5 * time.Second)
	if leader == nil {
		t.Fatal("no leader")
	}

	duration := 10 * time.Second
	interval := 1 * time.Second

	var ops atomic.Int64
	stopCh := make(chan struct{})
	var wg sync.WaitGroup

	// Writers
	for w := 0; w < 10; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			i := 0
			for {
				select {
				case <-stopCh:
					return
				default:
				}
				key := fmt.Sprintf("w%d-k%d", id, i)
				if leader.Put(key, "value") == nil {
					ops.Add(1)
				}
				i++
			}
		}(w)
	}

	// Reporter
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		var lastOps int64
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				current := ops.Load()
				delta := current - lastOps
				t.Logf("Throughput: %d ops/sec", delta)
				lastOps = current
			}
		}
	}()

	time.Sleep(duration)
	close(stopCh)
	wg.Wait()

	total := ops.Load()
	t.Logf("Total: %d ops in %v (%.2f ops/sec)", total, duration, float64(total)/duration.Seconds())
}
