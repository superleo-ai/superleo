package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"testing"

	rpcv1 "github.com/liliang-cn/cortexdb/v2/pkg/rpc/v1"
	"google.golang.org/grpc"
)

// recallBrain answers knowledge_memory_recall per namespace, as a shared
// brain does: each namespace has its own memories, the knowledge is the same.
type recallBrain struct {
	rpcv1.UnimplementedToolsServiceServer
	mu    sync.Mutex
	calls []string
}

func (b *recallBrain) CallTool(_ context.Context, req *rpcv1.CallToolRequest) (*rpcv1.CallToolResponse, error) {
	var args struct {
		Namespace string `json:"namespace"`
	}
	if err := json.Unmarshal([]byte(req.GetArgsJson()), &args); err != nil {
		return nil, err
	}
	b.mu.Lock()
	b.calls = append(b.calls, req.GetName()+" "+args.Namespace)
	b.mu.Unlock()
	score := map[string]float64{"hive": 0.4, "default": 0.8}[args.Namespace]
	return &rpcv1.CallToolResponse{ResultJson: fmt.Sprintf(`{
		"memories": [{"memory": {"id": "m-%[1]s", "scope": "global", "content": "memory in %[1]s"}, "score": %[2]g}],
		"knowledge": [{"knowledge_id": "beacon", "title": "Beacon", "snippet": "Beacon sends push", "score": 0.5}],
		"entities": ["Beacon", "%[1]s"],
		"context_pack": {"text": "ctx %[1]s"}
	}`, args.Namespace, score)}, nil
}

func startRecallBrain(t *testing.T) (*recallBrain, string) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	brain := &recallBrain{}
	srv := grpc.NewServer()
	rpcv1.RegisterToolsServiceServer(srv, brain)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	return brain, lis.Addr().String()
}

// A recall reads every namespace the store reads: memories merge by the
// server's score, a document comes once, and a memory found only through a
// read-only namespace stays read-only.
func TestReadThroughKnowledgeRecallReadsEveryNamespace(t *testing.T) {
	brain, addr := startRecallBrain(t)
	s, err := newReadThroughMemoryStore(addr, "", "hive", []string{sharedNamespaceDefault})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	res, err := s.KnowledgeRecall(context.Background(), "who runs push", 5, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(brain.calls) != 2 {
		t.Fatalf("calls = %v, want one recall per namespace", brain.calls)
	}
	if len(res.Memories) != 2 || res.Memories[0].ID != "m-default" || res.Memories[1].ID != "m-hive" {
		t.Fatalf("memories not merged by score: %+v", res.Memories)
	}
	if len(res.Knowledge) != 1 || res.Knowledge[0].ID != "beacon" {
		t.Fatalf("knowledge = %+v, want the document once", res.Knowledge)
	}
	if res.ContextText != "ctx hive" {
		t.Fatalf("context = %q, want the primary namespace's", res.ContextText)
	}
	if want := []string{"Beacon", "hive", "default"}; fmt.Sprint(res.Entities) != fmt.Sprint(want) {
		t.Fatalf("entities = %v, want %v", res.Entities, want)
	}
	if !s.isForeign("m-default") || s.isForeign("m-hive") {
		t.Fatal("only the default namespace's memory is read-only here")
	}
}
