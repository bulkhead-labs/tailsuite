// Package agent is the node agent: one small program in every lab node
// that runs listeners and probes (#7, decision 0020).
//
// The agent runs for the node's whole life, as its entrypoint, and owns the
// node's listeners. The host talks to it in sessions: each tailsuite
// process opens its own, through the sandbox's exec channel (decision
// 0019), so control traffic never crosses a lab network or the tailnet.
// Listeners don't belong to any session, because `tailsuite up` and
// `tailsuite test` are separate processes and the lab outlives both.
//
// The protocol is newline-delimited JSON. Each request carries an ID, its
// response carries the same ID, and requests run concurrently, so
// responses may come back in any order.
//
// This package uses only the standard library, to keep the agent small.
package agent

import (
	"encoding/json"
	"time"
)

// ProtocolVersion changes whenever a message changes incompatibly.
const ProtocolVersion = 1

// Ops a request can name.
const (
	OpHello        = "hello"
	OpSetListeners = "set_listeners"
	OpListeners    = "listeners"
	OpProbe        = "probe"
)

// Request is one message from the host.
type Request struct {
	ID    uint64     `json:"id"`
	Op    string     `json:"op"`
	Want  []Listener `json:"want,omitempty"` // set_listeners
	Probe *ProbeSpec `json:"probe,omitempty"`
}

// Response answers one request: either Error, or the result for its op.
type Response struct {
	ID        uint64     `json:"id"`
	Error     string     `json:"error,omitempty"`
	Hello     *Hello     `json:"hello,omitempty"`
	Listeners []Listener `json:"listeners,omitempty"` // set_listeners, listeners
	// Generation counts the changes to the node's listeners, for
	// set_listeners and listeners. A runner reads it before and after its
	// probes, on one session: if it moved, another process changed the
	// listeners under them, and the probes prove nothing (#8). It restarts
	// with the agent, so a lost session makes the results UNKNOWN too.
	Generation uint64       `json:"generation,omitempty"`
	Probe      *ProbeResult `json:"probe,omitempty"`
}

// Hello identifies the agent.
type Hello struct {
	Version int    `json:"version"`
	Node    string `json:"node"`
}

// Protocols a listener serves or a probe speaks.
const (
	ProtoTCP  = "tcp"
	ProtoHTTP = "http"
)

// Listener is one listener on the node: wanted, in a set_listeners
// request, or open, in a response.
type Listener struct {
	Proto string `json:"proto"`
	Port  int    `json:"port"`
	// Addr is the local address to bind. Empty means every interface, as
	// a service in production usually does.
	Addr string `json:"addr,omitempty"`
}

// ProbeSpec asks for one probe.
type ProbeSpec struct {
	Proto string `json:"proto"`
	Host  string `json:"host"`
	Port  int    `json:"port"`
	// Path is the HTTP request's path, a plain path with no query,
	// fragment or escape. Empty means "/".
	Path string `json:"path,omitempty"`
	// Timeout bounds the whole probe. Zero means DefaultProbeTimeout.
	Timeout Duration `json:"timeout,omitempty"`
}

// DefaultProbeTimeout bounds a probe whose spec sets no timeout.
const DefaultProbeTimeout = 3 * time.Second

// Outcome is how a probe ended. Each means something different to a test,
// so they're never merged:
type Outcome string

const (
	// Success: the connection opened, and for HTTP, a response came back.
	Success Outcome = "success"
	// Refused: something answered with a refusal. Usually that's the
	// target, with nothing listening on the port; a REJECT rule on the path
	// answers the same way. It isn't a drop, and a Tailscale filter only
	// drops, so it isn't the lab's deny either.
	Refused Outcome = "refused"
	// Timeout: no answer within the timeout. A Tailscale filter drops
	// denied packets silently, so a deny looks like this; so does a node
	// that's down.
	Timeout Outcome = "timeout"
	// Error: anything else, such as no route to the address. Detail says
	// what.
	Error Outcome = "error"
)

// ProbeResult is what one probe saw.
type ProbeResult struct {
	Outcome Outcome `json:"outcome"`
	// Detail is the error behind any outcome but Success.
	Detail string `json:"detail,omitempty"`
	// Connected says the TCP connection opened, even if the probe then
	// failed, such as an HTTP server that never answered.
	Connected bool `json:"connected"`
	// Peer is the node name the far end's tailsuite listener reported,
	// if it reported one. It proves which node answered.
	Peer string `json:"peer,omitempty"`
	// HTTPStatus is the response's status code, for HTTP.
	HTTPStatus int `json:"http_status,omitempty"`
	// Connect is how long the TCP connection took to open, if it did.
	Connect Duration `json:"connect,omitempty"`
	// Total is how long the whole probe took.
	Total Duration `json:"total"`
}

// Duration is a time.Duration that travels as a string, such as "1.5s".
type Duration time.Duration

// MarshalJSON implements json.Marshaler.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// UnmarshalJSON implements json.Unmarshaler.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}
