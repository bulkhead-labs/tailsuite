// Package control puts node agents in a sandbox's nodes and talks to them,
// over the sandbox's exec channel (decisions 0019 and 0020).
package control

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/bulkhead-labs/tailsuite/internal/agent"
	"github.com/bulkhead-labs/tailsuite/internal/sandbox"
)

// AgentPath is where the agent binary goes in every node.
const AgentPath = "/opt/tailsuite/tailsuite-agent"

// AgentFile is the agent binary as a file for sandbox.NodeSpec.Files.
func AgentFile(binary []byte) sandbox.File {
	return sandbox.File{Path: AgentPath, Mode: 0o755, Content: binary}
}

// Entrypoint is a node's entrypoint with the agent in front: the agent
// serves sessions, and runs command, if any, as the node's own process,
// exiting when it exits.
func Entrypoint(node string, command ...string) []string {
	e := []string{AgentPath, "run", "--node", node}
	if len(command) > 0 {
		e = append(append(e, "--"), command...)
	}
	return e
}

// Session is one session with a node's agent.
type Session struct {
	*agent.Client
	Node sandbox.Node

	stderr  *limitedBuffer
	exited  chan struct{}
	code    int
	execErr error
}

// Start opens a session with a node's agent and checks that it answers as
// that node. It waits for the agent to start listening, briefly. The
// session lasts until Close, or until ctx is cancelled; the agent and its
// listeners outlive it.
func Start(ctx context.Context, sb sandbox.Sandbox, node sandbox.Node) (*Session, error) {
	toAgent, fromHost := io.Pipe()
	fromAgent, toHost := io.Pipe()
	s := &Session{
		Client: agent.NewClient(fromAgent, fromHost),
		Node:   node,
		stderr: &limitedBuffer{max: 64 << 10},
		exited: make(chan struct{}),
	}
	// If the client stops reading (a bad line, or one too long), nothing
	// would drain the session's output, and Exec would block writing it.
	// Closing the pipe makes those writes fail, so Exec returns.
	go func() {
		<-s.Done()
		_ = fromAgent.CloseWithError(agent.ErrClosed)
	}()
	go func() {
		s.code, s.execErr = sb.Exec(ctx, node, sandbox.ExecSpec{
			Cmd:    []string{AgentPath, "connect"},
			Stdin:  toAgent,
			Stdout: toHost,
			Stderr: s.stderr,
		})
		// Exec may return while its stdin copy is still reading: closing
		// the pipe ends it (sandbox.ExecSpec).
		_ = toAgent.Close()
		_ = toHost.Close()
		close(s.exited)
	}()
	hello, err := s.Hello(ctx)
	if err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("agent in node %s: %w%s", node.Name, err, s.stderrNote())
	}
	if hello.Node != node.Name {
		_ = s.Close()
		return nil, fmt.Errorf("the agent in node %s says it's %q", node.Name, hello.Node)
	}
	return s, nil
}

// Close ends the session and waits for its exec to finish. The agent and
// its listeners go on.
func (s *Session) Close() error {
	_ = s.Client.Close()
	<-s.exited
	if s.execErr != nil {
		return fmt.Errorf("agent session in node %s: %w", s.Node.Name, s.execErr)
	}
	if s.code != 0 {
		return fmt.Errorf("agent session in node %s exited %d%s", s.Node.Name, s.code, s.stderrNote())
	}
	return nil
}

func (s *Session) stderrNote() string {
	if msg := strings.TrimSpace(s.stderr.String()); msg != "" {
		return ": " + msg
	}
	return ""
}

// limitedBuffer keeps the first max bytes written to it.
type limitedBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := b.max - len(b.buf); room > 0 {
		b.buf = append(b.buf, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
