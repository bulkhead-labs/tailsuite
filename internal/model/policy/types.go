// Package policy reads a Tailscale policy file the way decision 0014 says:
// the user's file, as read, is the truth; a typed copy, parsed from it, is
// read-only; and every key the typed copy doesn't understand is listed.
//
// The types cover every section of Tailscale's policy file reference
// (checked 2026-10-25), and the official Go client's (v2.12.0), which
// adds a few the reference doesn't. Parsing a section isn't supporting it:
// what each engine supports is 0016's capability matrix.
package policy

import (
	"encoding/json"
	"errors"
)

// Policy is the typed copy of a policy file. It's read-only: deploys and
// engines work from the user's bytes, patched (0014), never from this.
//
// No field has omitempty. Encoded again, every known key is present, so
// the comparison that finds unknown keys never relies on omitempty.
type Policy struct {
	ACLs              []ACL                      `json:"acls"`
	Grants            []Grant                    `json:"grants"`
	SSH               []SSHRule                  `json:"ssh"`
	Groups            map[string][]string        `json:"groups"`
	TagOwners         map[string][]string        `json:"tagOwners"`
	Hosts             map[string]string          `json:"hosts"`
	IPSets            map[string][]string        `json:"ipsets"`
	Postures          map[string]Posture         `json:"postures"`
	DefaultSrcPosture []string                   `json:"defaultSrcPosture"`
	AutoApprovers     AutoApprovers              `json:"autoApprovers"`
	NodeAttrs         []NodeAttr                 `json:"nodeAttrs"`
	Tests             []Test                     `json:"tests"`
	SSHTests          []SSHTest                  `json:"sshTests"`
	AttrConfig        map[string]AttrConfig      `json:"attrConfig"`
	ExternalTailnets  map[string]ExternalTailnet `json:"externalTailnets"`

	// Network policy options.
	DERPMap             json.RawMessage `json:"derpMap"` // carried as written, not interpreted
	DisableIPv4         bool            `json:"disableIPv4"`
	OneCGNATRoute       string          `json:"oneCGNATRoute"`
	RandomizeClientPort bool            `json:"randomizeClientPort"`
}

// ACL is a rule in the legacy acls section.
type ACL struct {
	Action     string   `json:"action"`
	Src        []string `json:"src"`
	Dst        []string `json:"dst"` // host:port entries
	Proto      string   `json:"proto"`
	SrcPosture []string `json:"srcPosture"`

	// Legacy spellings: users for src, ports for dst.
	Users []string `json:"users"`
	Ports []string `json:"ports"`
}

// Grant is a rule in the grants section.
type Grant struct {
	Src        []string `json:"src"`
	Dst        []string `json:"dst"`
	IP         []string `json:"ip"`
	App        AppCaps  `json:"app"`
	SrcPosture []string `json:"srcPosture"`
	Via        []string `json:"via"`
}

// AppCaps are a grant's application capabilities, by name. Their payloads
// belong to each application, and are carried as written.
type AppCaps map[string][]json.RawMessage

// SSHRule is a rule in the ssh section.
type SSHRule struct {
	Action          string   `json:"action"`
	Src             []string `json:"src"`
	Dst             []string `json:"dst"`
	Users           []string `json:"users"` // logins on the host, not tailnet identities
	CheckPeriod     string   `json:"checkPeriod"`
	AcceptEnv       []string `json:"acceptEnv"`
	SrcPosture      []string `json:"srcPosture"`
	Recorder        []string `json:"recorder"`
	EnforceRecorder bool     `json:"enforceRecorder"`
}

// Posture is one entry of the postures section, in either syntax: the
// object syntax, with assertions and onFailure, or the legacy list of
// assertions. It's encoded again in the syntax it was read in.
type Posture struct {
	Assertions []string
	OnFailure  *PostureOnFailure
	// Legacy is true for the list syntax.
	Legacy bool
}

// PostureOnFailure says how a failing posture is surfaced.
type PostureOnFailure struct {
	ShowAlerts     []string `json:"showAlerts"`
	EndUserMessage string   `json:"endUserMessage"`
}

type postureObject struct {
	Assertions []string          `json:"assertions"`
	OnFailure  *PostureOnFailure `json:"onFailure"`
}

// UnmarshalJSON reads either syntax.
func (p *Posture) UnmarshalJSON(b []byte) error {
	var list []string
	if err := json.Unmarshal(b, &list); err == nil {
		*p = Posture{Assertions: list, Legacy: true}
		return nil
	}
	var obj postureObject
	if err := json.Unmarshal(b, &obj); err != nil {
		return errors.New("a posture is a list of assertions, or an object with assertions")
	}
	*p = Posture{Assertions: obj.Assertions, OnFailure: obj.OnFailure}
	return nil
}

// MarshalJSON writes the syntax the posture was read in.
func (p Posture) MarshalJSON() ([]byte, error) {
	if p.Legacy {
		return json.Marshal(p.Assertions)
	}
	return json.Marshal(postureObject{Assertions: p.Assertions, OnFailure: p.OnFailure})
}

// AutoApprovers is the autoApprovers section.
type AutoApprovers struct {
	Routes   map[string][]string `json:"routes"`
	ExitNode []string            `json:"exitNode"`
	Services map[string][]string `json:"services"`
}

// NodeAttr is an entry of the nodeAttrs section.
type NodeAttr struct {
	Target []string `json:"target"`
	Attr   []string `json:"attr"`
	// App holds application capabilities, such as app connectors, whose
	// payloads belong to each application, as a grant's do.
	App    AppCaps  `json:"app"`
	IPPool []string `json:"ipPool"`
}

// Test is an entry of the tests section: Tailscale's own assertions,
// evaluated against the rules on every save.
type Test struct {
	Src             string         `json:"src"`
	Proto           string         `json:"proto"`
	Accept          []string       `json:"accept"` // host:port entries
	Deny            []string       `json:"deny"`   // host:port entries
	SrcPostureAttrs map[string]any `json:"srcPostureAttrs"`

	// Legacy spellings: user for src, allow for accept.
	User  string   `json:"user"`
	Allow []string `json:"allow"`
}

// SSHTest is an entry of the sshTests section.
type SSHTest struct {
	Src             string         `json:"src"`
	Dst             []string       `json:"dst"`
	Accept          []string       `json:"accept"` // logins
	Check           []string       `json:"check"`  // logins
	Deny            []string       `json:"deny"`   // logins
	SrcPostureAttrs map[string]any `json:"srcPostureAttrs"`
}

// AttrConfig configures a custom device attribute.
type AttrConfig struct {
	Type             string   `json:"type"`
	AllowSetByNode   bool     `json:"allowSetByNode"`
	BroadcastToPeers []string `json:"broadcastToPeers"`
}

// ExternalTailnet configures access from another tailnet.
type ExternalTailnet struct {
	ExternalID                string   `json:"externalID"`
	AllowIncomingConnections  bool     `json:"allowIncomingConnections"`
	AllowExternalReferencesTo []string `json:"allowExternalReferencesTo"`
}
