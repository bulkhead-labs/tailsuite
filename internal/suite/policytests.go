package suite

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bulkhead-labs/tailsuite/internal/model/policy"
)

// PolicyTests maps the policy's own tests and sshTests onto tailsuite's
// (0015), so every suite runs them with real traffic:
//
//   - src becomes from; each accept or deny entry (host:port) becomes one
//     test, with its to, port and expect;
//   - srcPostureAttrs becomes when;
//   - an omitted proto means TCP and UDP, as Tailscale reads it;
//   - in sshTests, each dst and each login under accept, check or deny
//     becomes one SSH test, with accept read as allow.
func PolicyTests(f *policy.File) []Test {
	if f == nil {
		return nil
	}
	ptr := func(canonical string) string {
		if p, ok := f.FilePointer(canonical); ok {
			return p
		}
		return canonical
	}
	var out []Test
	for i, pt := range f.Policy.Tests {
		src := pt.Src
		if src == "" {
			src = pt.User // the legacy spelling
		}
		protos := protocols(pt.Proto)
		when := postureWhen(pt.SrcPostureAttrs)
		add := func(field string, entries []string, expect string) {
			for j, e := range entries {
				to, port := splitHostPort(e)
				base := fmt.Sprintf("/tests/%d/%s/%d", i, field, j)
				out = append(out, Test{
					Name:      fmt.Sprintf("policy tests[%d]: %s → %s", i, src, e),
					From:      src,
					To:        to,
					Port:      port,
					Expect:    expect,
					When:      when,
					Source:    SourcePolicy,
					Where:     ptr(base),
					Protocols: protos,
				})
			}
		}
		add("accept", pt.Accept, Allow)
		add("allow", pt.Allow, Allow)
		add("deny", pt.Deny, Deny)
	}
	for i, st := range f.Policy.SSHTests {
		when := postureWhen(st.SrcPostureAttrs)
		for j, dst := range st.Dst {
			for _, set := range []struct {
				field, expect string
				logins        []string
			}{{"accept", Allow, st.Accept}, {"check", Check, st.Check}, {"deny", Deny, st.Deny}} {
				for _, login := range set.logins {
					out = append(out, Test{
						Name:      fmt.Sprintf("policy sshTests[%d]: %s → %s as %s", i, st.Src, dst, login),
						From:      st.Src,
						To:        dst,
						Port:      22,
						SSH:       login,
						Expect:    set.expect,
						When:      when,
						Source:    SourcePolicy,
						Where:     ptr(fmt.Sprintf("/sshTests/%d/dst/%d", i, j)),
						Protocols: []string{"tcp"},
					})
				}
			}
		}
	}
	return out
}

// protocols is what a policy test's proto asserts. Omitted, Tailscale
// checks "for either TCP or UDP access".
func protocols(proto string) []string {
	switch p := strings.ToLower(proto); p {
	case "":
		return []string{"tcp", "udp"}
	case "6":
		return []string{"tcp"}
	case "17":
		return []string{"udp"}
	default:
		return []string{p}
	}
}

// postureWhen carries srcPostureAttrs as conditions, keyed as written
// (node:os, custom:…), so the needs computation can see them.
func postureWhen(attrs map[string]any) map[string]string {
	if len(attrs) == 0 {
		return nil
	}
	w := map[string]string{}
	for k, v := range attrs {
		w[k] = fmt.Sprint(v)
	}
	return w
}

// splitHostPort splits a test entry, host:port. A port that isn't one
// number, such as * or a range, is 0: such a test can't be probed, and is
// UNKNOWN.
func splitHostPort(e string) (string, int) {
	i := strings.LastIndex(e, ":")
	if i < 0 {
		return e, 0
	}
	host := strings.TrimSuffix(strings.TrimPrefix(e[:i], "["), "]")
	port, err := strconv.Atoi(e[i+1:])
	if err != nil || port < 1 || port > 65535 {
		return host, 0
	}
	return host, port
}
