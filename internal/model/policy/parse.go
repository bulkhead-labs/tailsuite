package policy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/tailscale/hujson"
)

// File is a policy file: the user's bytes, which are the truth, and the
// typed copy parsed from them.
type File struct {
	// Raw is the file exactly as read. Deploy ships it unchanged, and each
	// engine gets it plus a patch (0014).
	Raw []byte

	// Policy is the typed copy. It's read-only.
	Policy Policy

	// Unknown lists, in file order, every key the typed copy doesn't
	// understand, and every key written twice.
	Unknown []UnknownKey

	tree hujson.Value
	// canon maps each place the typed copy understood, as a pointer in its
	// own spelling, to the same place in the file's spelling.
	canon map[string]string
}

// UnknownKey is a key in the file the typed copy didn't take in.
type UnknownKey struct {
	// Pointer is an RFC 6901 pointer to the key, in the file's own
	// spelling.
	Pointer string `json:"pointer"`
	// Reason says why: "not understood", or "duplicate key", for an
	// occurrence a later one overrides.
	Reason string `json:"reason"`
	// Start and End are the key's member's byte offsets in Raw.
	Start int `json:"start"`
	End   int `json:"end"`
}

// Reasons an UnknownKey gives.
const (
	NotUnderstood = "not understood"
	DuplicateKey  = "duplicate key" // the typed copy holds only the last occurrence
)

// Parse reads a policy file. An error means the file isn't HuJSON, or a
// known section has the wrong shape; unknown keys aren't errors, they're
// listed.
func Parse(raw []byte) (*File, error) {
	tree, err := hujson.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("policy: %w", err)
	}
	std := tree.Clone()
	std.Standardize()
	f := &File{Raw: slices.Clone(raw), tree: tree, canon: map[string]string{"": ""}}

	// Go's decoder decodes a repeated key into the earlier occurrence: maps
	// and objects merge, and list elements are reused. So the typed copy
	// would hold a policy the file doesn't state. A first decode finds
	// which occurrences are overridden, they're removed from a copy, and
	// the typed copy is decoded from that: the last occurrence, alone.
	// An overridden occurrence may have the wrong type; the first decode
	// only needs the structure, so a type error doesn't stop it. The
	// second, of the last occurrences alone, still refuses one.
	first, err := decode(std)
	var typeErr *json.UnmarshalTypeError
	if err != nil && !errors.As(err, &typeErr) {
		return nil, err
	}
	var drop [][]int
	overridden(&tree, first, nil, &drop)
	for i := len(drop) - 1; i >= 0; i-- { // deepest and latest first, so indices hold
		remove(&std, drop[i])
	}
	understood, err := decodeInto(std, &f.Policy)
	if err != nil {
		return nil, err
	}
	f.walk(&f.tree, understood, "", "", true)
	return f, nil
}

// decode decodes a standardized tree into a throwaway Policy, and returns
// what the Policy understood, encoded again as a generic value. After a
// type error, which Go's decoder reports only once it has decoded the
// rest, it returns that error with what was understood.
func decode(std hujson.Value) (any, error) {
	var p Policy
	return decodeLenient(std, &p)
}

// decodeInto decodes a standardized tree into p, and returns what p
// understood, encoded again as a generic value.
func decodeInto(std hujson.Value, p *Policy) (any, error) {
	understood, err := decodeLenient(std, p)
	if err != nil {
		return nil, err
	}
	return understood, nil
}

func decodeLenient(std hujson.Value, p *Policy) (any, error) {
	// UseNumber keeps numbers, such as posture attribute values, exactly
	// as written.
	dec := json.NewDecoder(bytes.NewReader(std.Pack()))
	dec.UseNumber()
	decodeErr := dec.Decode(p)
	var typeErr *json.UnmarshalTypeError
	if decodeErr != nil && !errors.As(decodeErr, &typeErr) {
		return nil, fmt.Errorf("policy: %w", decodeErr)
	}
	encoded, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("policy: encoding the typed copy: %w", err)
	}
	var understood any
	udec := json.NewDecoder(bytes.NewReader(encoded))
	udec.UseNumber()
	if err := udec.Decode(&understood); err != nil {
		return nil, fmt.Errorf("policy: decoding the typed copy: %w", err)
	}
	if decodeErr != nil {
		return understood, fmt.Errorf("policy: %w", decodeErr)
	}
	return understood, nil
}

// overridden collects the index paths of members a later occurrence of
// the same key overrides, by the same rule as walk.
func overridden(v *hujson.Value, understood any, path []int, out *[][]int) {
	switch node := v.Value.(type) {
	case *hujson.Object:
		known, _ := understood.(map[string]any)
		keys := make([]string, len(node.Members))
		found := make([]bool, len(node.Members))
		last := map[string]int{}
		for i := range node.Members {
			keys[i], _, found[i] = lookupFold(known, memberName(&node.Members[i]))
			if found[i] {
				last[keys[i]] = i
			}
		}
		for i := range node.Members {
			here := append(slices.Clone(path), i)
			switch {
			case !found[i]:
			case last[keys[i]] != i:
				*out = append(*out, here)
			default:
				overridden(&node.Members[i].Value, known[keys[i]], here, out)
			}
		}
	case *hujson.Array:
		list, _ := understood.([]any)
		for i := range node.Elements {
			var sub any
			if i < len(list) {
				sub = list[i]
			}
			overridden(&node.Elements[i], sub, append(slices.Clone(path), i), out)
		}
	}
}

// remove deletes the object member at an index path.
func remove(v *hujson.Value, path []int) {
	for _, i := range path[:len(path)-1] {
		switch node := v.Value.(type) {
		case *hujson.Object:
			v = &node.Members[i].Value
		case *hujson.Array:
			v = &node.Elements[i]
		default:
			return
		}
	}
	if obj, ok := v.Value.(*hujson.Object); ok {
		i := path[len(path)-1]
		obj.Members = slices.Delete(obj.Members, i, i+1)
	}
}

// walk compares the file's tree with what the typed copy understood,
// encoded again. A key the typed copy has no key for, at the same place,
// is unknown. Keys match ignoring case, as Go's decoder does, because
// older policies capitalise them (ACLs, Groups). canonical and ptr are the
// same place, in the typed copy's spelling and the file's.
//
// addressable is false under a key whose exact name the object repeats:
// an RFC 6901 pointer can't tell the occurrences apart, so no pointer is
// recorded there. The repeat is listed as a duplicate, which makes an
// identity rewrite REFUSED (0014).
func (f *File) walk(v *hujson.Value, understood any, canonical, ptr string, addressable bool) {
	if addressable {
		f.canon[canonical] = ptr
	}
	switch node := v.Value.(type) {
	case *hujson.Object:
		known, _ := understood.(map[string]any)
		// Two keys are duplicates when they land on the same key of the
		// typed copy: a field written twice, maybe in two spellings (acls,
		// ACLs). The decoder keeps the last, so the earlier ones are
		// flagged. Map keys are case-sensitive: group:Eng and group:eng
		// are two keys.
		keys := make([]string, len(node.Members))
		found := make([]bool, len(node.Members))
		last := map[string]int{}
		names := map[string]int{}
		for i := range node.Members {
			name := memberName(&node.Members[i])
			names[name]++
			keys[i], _, found[i] = lookupFold(known, name)
			if found[i] {
				last[keys[i]] = i
			}
		}
		for i := range node.Members {
			m := &node.Members[i]
			name := memberName(m)
			here := ptr + "/" + escape(name)
			switch {
			case !found[i]:
				f.Unknown = append(f.Unknown, UnknownKey{Pointer: here, Reason: NotUnderstood, Start: m.Name.StartOffset, End: m.Value.EndOffset})
			case last[keys[i]] != i:
				winner := memberName(&node.Members[last[keys[i]]])
				f.Unknown = append(f.Unknown, UnknownKey{Pointer: here, Reason: DuplicateKey + " (overridden by " + winner + ")", Start: m.Name.StartOffset, End: m.Value.EndOffset})
			default:
				f.walk(&m.Value, known[keys[i]], canonical+"/"+escape(keys[i]), here, addressable && names[name] == 1)
			}
		}
	case *hujson.Array:
		list, _ := understood.([]any)
		for i := range node.Elements {
			var sub any
			if i < len(list) {
				sub = list[i]
			}
			idx := "/" + strconv.Itoa(i)
			f.walk(&node.Elements[i], sub, canonical+idx, ptr+idx, addressable)
		}
	}
}

func memberName(m *hujson.ObjectMember) string {
	lit, _ := m.Name.Value.(hujson.Literal)
	return lit.String()
}

// lookupFold finds a key in an object ignoring case, preferring an exact
// match, and returns the key it found.
func lookupFold(obj map[string]any, name string) (string, any, bool) {
	if obj == nil {
		return "", nil, false
	}
	if v, ok := obj[name]; ok {
		return name, v, true
	}
	for k, v := range obj {
		if strings.EqualFold(k, name) {
			return k, v, true
		}
	}
	return "", nil, false
}

// escape encodes a key for an RFC 6901 pointer.
func escape(key string) string {
	return strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
}

// Span returns the byte offsets in Raw of the value a pointer names, in
// the file's own spelling, so the UI can point at an exact rule.
func (f *File) Span(pointer string) (start, end int, ok bool) {
	v := f.tree.Find(pointer)
	if v == nil {
		return 0, 0, false
	}
	return v.StartOffset, v.EndOffset, true
}

// FilePointer turns a pointer in the typed copy's spelling (/acls/0/src)
// into the file's own (/ACLs/0/src), so a patch names what the file says.
// ok is false if the typed copy took nothing from that place.
func (f *File) FilePointer(canonical string) (string, bool) {
	ptr, ok := f.canon[canonical]
	return ptr, ok
}
