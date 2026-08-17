package report

import (
	"encoding/xml"
	"fmt"
	"io"
)

// M3d.1 (#51): the JUnit reporter, against the vendored Jenkins-dialect
// schema (docs/schema/junit-jenkins.xsd). Every state is mapped, so no
// finding class disappears from a CI pane: survivors and the structurally
// unassertable fail with diagnosis and fix guidance, Killed and KilledByError
// pass, NoCoverage, Unobservable, Ignored and Invalid are skipped cases with
// the state as the skip message, Timeout errors, and operational failures
// error at suite level.

// junitDocument is the testsuites root element.
type junitDocument struct {
	XMLName xml.Name     `xml:"testsuites"`
	Name    string       `xml:"name,attr"`
	Tests   int          `xml:"tests,attr"`
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
	Failure   *junitOutcome `xml:"failure,omitempty"`
	Error     *junitOutcome `xml:"error,omitempty"`
	Skipped   *junitOutcome `xml:"skipped,omitempty"`
}

type junitOutcome struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr,omitempty"`
	Body    string `xml:",chardata"`
}

// WriteJUnit renders the report in the vendored Jenkins dialect.
func WriteJUnit(writer io.Writer, value Report) error {
	suite := junitSuite{
		Name: sarifTool, Tests: 0, Failures: 0, Errors: 0, Skipped: 0, Cases: []junitCase{},
	}

	for _, mutant := range value.Mutants {
		entry := junitCase{
			Name:      mutant.Operator + " @ " + mutant.Site,
			Classname: mutant.Module,
			Failure:   nil, Error: nil, Skipped: nil,
		}

		//nolint:exhaustive // the default arm is the skip mapping for every remaining state.
		switch mutant.State {
		case Survived, StructurallyUnassertable:
			suite.Failures++
			entry.Failure = &junitOutcome{
				Message: findingMessage(mutant),
				Type:    string(mutant.State),
				Body:    findingBody(mutant),
			}
		case Killed, KilledByError:
			// A pass: the element stays empty.
		case Timeout:
			suite.Errors++
			entry.Error = &junitOutcome{
				Message: "the mutant exceeded its execution budget",
				Type:    string(Timeout),
				Body:    "",
			}
		default:
			// NoCoverage, Unobservable, Ignored, Invalid and Pending are all
			// skipped cases with the state as the message.
			suite.Skipped++
			entry.Skipped = &junitOutcome{Message: string(mutant.State), Type: "", Body: ""}
		}

		suite.Tests++
		suite.Cases = append(suite.Cases, entry)
	}

	// Operational failures error at suite level: an unevaluated mutant is
	// never a verdict, and it must not disappear from the pane either.
	for _, failure := range value.Errors {
		suite.Tests++
		suite.Errors++
		suite.Cases = append(suite.Cases, junitCase{
			Name:      "operational failure @ " + failure.Site,
			Classname: sarifTool,
			Failure:   nil,
			Error: &junitOutcome{
				Message: failure.Message, Type: "OperationalFailure", Body: "",
			},
			Skipped: nil,
		})
	}

	document := junitDocument{
		XMLName: xml.Name{Space: "", Local: "testsuites"},
		Name:    sarifTool,
		Tests:   suite.Tests,
		Suites:  []junitSuite{suite},
	}

	if _, err := io.WriteString(writer, xml.Header); err != nil {
		return fmt.Errorf("writing JUnit header: %w", err)
	}

	encoder := xml.NewEncoder(writer)
	encoder.Indent("", "  ")

	if err := encoder.Encode(document); err != nil {
		return fmt.Errorf("encoding JUnit report: %w", err)
	}

	if _, err := io.WriteString(writer, "\n"); err != nil {
		return fmt.Errorf("writing JUnit trailer: %w", err)
	}

	return nil
}

func findingMessage(mutant Mutant) string {
	if mutant.Verdict == nil {
		return string(mutant.State)
	}

	message := string(mutant.State)
	if mutant.Verdict.Diagnosis != "" {
		message += ": " + string(mutant.Verdict.Diagnosis)
	}

	return message
}

func findingBody(mutant Mutant) string {
	if mutant.Verdict == nil {
		return ""
	}

	return mutant.Verdict.Message + "\nFix: " + mutant.Verdict.Fix
}
