package runner

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// WriteJSON writes the run as JSON, the one shape the CLI, CI and the UI
// all read (0017).
func WriteJSON(w io.Writer, r *Run) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// WriteText writes the run for a person: every test with its result, then
// the unproven ones listed apart, so they're never read as passes (0015).
func WriteText(w io.Writer, r *Run) error {
	var b strings.Builder
	fmt.Fprintf(&b, "tailsuite test: %s, profile %s", r.Version, r.Profile)
	if r.Strict {
		b.WriteString(", strict")
	}
	b.WriteString("\n\n")
	for _, t := range r.Tests {
		if t.Skipped != "" {
			continue
		}
		fmt.Fprintf(&b, "  %-7s %s\n", t.Result, t.Name)
		if t.Result == Fail {
			fmt.Fprintf(&b, "          %s\n", t.Reason)
		}
	}
	var unproven []TestResult
	for _, t := range r.Tests {
		if t.Result == Unknown || t.Result == Refused {
			unproven = append(unproven, t)
		}
	}
	if len(unproven) > 0 {
		b.WriteString("\nNot proven in this lab (never a pass, never a deny):\n")
		for _, t := range unproven {
			fmt.Fprintf(&b, "  %-7s %s\n          %s\n", t.Result, t.Name, t.Reason)
		}
	}
	s := r.Summary
	fmt.Fprintf(&b, "\n%d passed, %d failed, %d unknown, %d refused, %d skipped\n", s.Pass, s.Fail, s.Unknown, s.Refused, s.Skipped)
	for _, warn := range r.Warnings {
		fmt.Fprintf(&b, "warning: %s\n", warn)
	}
	switch r.Exit {
	case 1:
		b.WriteString("FAIL\n")
	case 2:
		if s.Pass+s.Fail == 0 {
			b.WriteString("nothing was proven\n")
		} else {
			b.WriteString("unproven results under strict\n")
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

type junitSuites struct {
	XMLName xml.Name     `xml:"testsuites"`
	Suites  []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Name     string      `xml:"name,attr"`
	Tests    int         `xml:"tests,attr"`
	Failures int         `xml:"failures,attr"`
	Errors   int         `xml:"errors,attr"`
	Skipped  int         `xml:"skipped,attr"`
	Cases    []junitCase `xml:"testcase"`
}

type junitCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Failure   *junitMessage `xml:"failure,omitempty"`
	Error     *junitMessage `xml:"error,omitempty"`
	Skipped   *junitMessage `xml:"skipped,omitempty"`
}

type junitMessage struct {
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}

// WriteJUnit writes the run as JUnit XML (0015). FAIL is a <failure>.
// UNKNOWN and REFUSED are skipped, with the reason; under strict they're
// an <error>, so they stay distinct from a FAIL. A test the selection left
// out is skipped too.
func WriteJUnit(w io.Writer, r *Run) error {
	s := junitSuite{Name: "tailsuite " + r.Profile + " on " + r.Engine}
	for _, t := range r.Tests {
		c := junitCase{Name: t.Name, Classname: "tailsuite." + t.Source}
		msg := &junitMessage{Message: string(t.Result) + ": " + t.Reason, Text: t.Where}
		switch {
		case t.Skipped != "":
			c.Skipped = &junitMessage{Message: "skipped: " + t.Skipped}
			s.Skipped++
		case t.Result == Fail:
			c.Failure = msg
			s.Failures++
		case (t.Result == Unknown || t.Result == Refused) && r.Strict:
			c.Error = msg
			s.Errors++
		case t.Result == Unknown || t.Result == Refused:
			c.Skipped = msg
			s.Skipped++
		}
		s.Cases = append(s.Cases, c)
		s.Tests++
	}
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(junitSuites{Suites: []junitSuite{s}}); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}
