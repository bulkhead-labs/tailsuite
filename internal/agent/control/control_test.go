package control

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bulkhead-labs/tailsuite/internal/agent"
	"github.com/bulkhead-labs/tailsuite/internal/sandbox"
)

// floodingSandbox's Exec answers hello, sends a line the client can't
// read, then writes output until a write fails, as the sandbox's output
// copy does.
type floodingSandbox struct{}

func (floodingSandbox) RunID() string { return "0a1b2c3d" }
func (floodingSandbox) CreateNetwork(context.Context, sandbox.NetworkSpec) (sandbox.Network, error) {
	return sandbox.Network{}, nil
}
func (floodingSandbox) StartNode(context.Context, sandbox.NodeSpec) (sandbox.Node, error) {
	return sandbox.Node{}, nil
}
func (floodingSandbox) Teardown(context.Context) error { return nil }

func (floodingSandbox) Exec(_ context.Context, node sandbox.Node, spec sandbox.ExecSpec) (int, error) {
	in := bufio.NewScanner(spec.Stdin)
	if !in.Scan() {
		return -1, errors.New("no hello")
	}
	var req agent.Request
	_ = json.Unmarshal(in.Bytes(), &req)
	resp, _ := json.Marshal(agent.Response{ID: req.ID, Hello: &agent.Hello{Version: agent.ProtocolVersion, Node: node.Name}})
	if _, err := spec.Stdout.Write(append(resp, '\n')); err != nil {
		return -1, err
	}
	if _, err := spec.Stdout.Write([]byte("not json\n")); err != nil {
		return -1, err
	}
	chunk := []byte(strings.Repeat("x", 64<<10))
	for {
		if _, err := spec.Stdout.Write(chunk); err != nil {
			return -1, err
		}
	}
}

// When the client stops reading, the session's output is closed, so the
// exec ends instead of blocking on a write nobody reads. Without that,
// Close never returns, even after the context ends.
func TestCloseWhenTheClientStopsReading(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := Start(ctx, floodingSandbox{}, sandbox.Node{Name: "n", ID: "x"})
	if err != nil {
		t.Fatal(err)
	}
	<-s.Done()
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close is stuck: the exec is blocked writing output nobody reads")
	}
}
