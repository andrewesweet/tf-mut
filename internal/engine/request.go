package engine

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/andrewesweet/tf-mut/internal/mutation"
)

// Request is the closed set of commands the engine performs. The unexported
// method keeps the set closed: no package outside engine can add a command,
// and the request's type — not a flag on it — is the command. An inapplicable
// combination of command and options is therefore not representable through
// the seam at all.
type Request interface{ isRequest() }

var errInvalidRequest = errors.New("invalid engine request")

// mode is the internal projection of the request type: which command the
// settings serve. It is unexported, so nothing outside engine can name one —
// the request types are the only writers, through the settings methods below,
// and the pipeline's mode-dependent steps (the preview shortcuts, the suggest
// leg, the curate posture) read it from there.
type mode int

const (
	// gradeMode grades a population and reports its verdicts. It is the zero
	// value, because a zero settings value has always meant an ordinary run.
	gradeMode mode = iota
	// previewMode describes a population without executing anything.
	previewMode
	// suggestMode grades, then generates and verifies assertions for the
	// survivors.
	suggestMode
	// characteriseMode scaffolds, harvests and pins a first suite.
	characteriseMode
	// todosMode lists the open judgement points, running no Terraform.
	todosMode
	// curateMode grades a full population and reports redundancy.
	curateMode
)

// Common carries the options shared by every engine command.
type Common struct {
	ModuleDir               string
	TestDirectory           string
	Jobs                    int
	TimeoutFactor           float64
	TimeoutFloor            time.Duration
	AllowRealInfrastructure bool
	AllowUnsandboxedEffects bool
	// FailureTripThreshold stops a run after this many consecutive
	// operational failures. Zero — the zero value a caller leaves untouched —
	// selects the documented default.
	FailureTripThreshold int
	TerraformBinary      string
	Env                  []string
	WorkDir              string
	ToolVersion          string
	SetFlags             []string
	// CPUProfile names the file the command's own CPU profile is written to.
	// Empty — the default — profiles nothing. It is a diagnostic for the
	// person tuning tf-mut itself, not part of any report.
	CPUProfile string
	// OTLPEndpoint is the OTLP/HTTP endpoint the run's spans are exported to.
	// Empty — the default — falls back to OTEL_EXPORTER_OTLP_ENDPOINT.
	// Tracing is disabled when neither supplies an endpoint.
	OTLPEndpoint string
	// Progress receives periodic long-run progress lines and tracing and
	// CPU-profile warnings. Nil — the default — discards both. Run serializes
	// writes within one invocation and stops writing before it returns.
	// The CLI passes its stderr writer; callers must keep report output separate.
	Progress io.Writer
	// ProgressIntervalForTesting overrides the cadence for engine-seam tests.
	// Zero selects the default ten seconds; it is honoured only when Progress is set.
	ProgressIntervalForTesting time.Duration
}

// Population selects the mutants a command acts on.
type Population struct {
	TestSelection      []string
	Tier               mutation.Tier
	IncludeOperators   []string
	ExcludeOperators   []string
	ExcludePaths       []string
	ExcludeResources   []string
	Since              string
	SamplePercent      float64
	HasSample          bool
	SampleSeed         int64
	GeneratedFunctions bool
	// Packs names the domain packs to enable, merged as a union with the
	// configured `operators { packs }` list and deduplicated by name.
	Packs []string
}

// Gate carries the acceptance policy applied to a completed population.
type Gate struct {
	MinScore             float64
	HasMinScore          bool
	AllowIncompleteScore bool
	AllowSampledGate     bool
	FailOnNew            bool
	WriteBaseline        bool
	BaselinePath         string
}

// RunRequest grades a selected population and applies its acceptance gates.
type RunRequest struct {
	Common
	Population
	Gate

	NoCache bool
}

// PreviewRequest describes a population without executing it.
type PreviewRequest struct {
	Common
	Population
}

// SuggestRequest generates and optionally applies assertions for survivors.
type SuggestRequest struct {
	Common
	Population
	Gate

	NoCache     bool
	DryRun      bool
	SurvivorIDs []string
	Apply       []string
	ApplyAll    bool
}

// CharacteriseRequest generates a first test suite for an untested module.
type CharacteriseRequest struct {
	Common

	PinRung  string
	Write    bool
	Force    bool
	UntilDry bool
	Resume   bool
	Answers  []string
}

// TodosRequest lists or resolves characterisation judgement points.
type TodosRequest struct {
	Common

	PinRung string
	Answers []string
	Resume  bool
}

// CurateRequest reports redundant assertions from an authoritative population.
type CurateRequest struct {
	Common
	Gate

	NoCache bool
}

func (RunRequest) isRequest()          {}
func (PreviewRequest) isRequest()      {}
func (SuggestRequest) isRequest()      {}
func (CharacteriseRequest) isRequest() {}
func (TodosRequest) isRequest()        {}
func (CurateRequest) isRequest()       {}

// settingsFor validates the request and produces the settings value its type
// names. This is the one route from the closed request set to the internal
// settings, so the mode the pipeline reads can never disagree with the type
// the caller passed.
func settingsFor(request Request) (settings, error) {
	switch typed := request.(type) {
	case nil:
		return settings{}, fmt.Errorf("%w: request must not be nil", errInvalidRequest)
	case RunRequest:
		return typed.settings(), nil
	case *RunRequest:
		if typed == nil {
			return settings{}, fmt.Errorf("%w: %T is nil", errInvalidRequest, request)
		}

		return typed.settings(), nil
	case PreviewRequest:
		return typed.settings(), nil
	case *PreviewRequest:
		if typed == nil {
			return settings{}, fmt.Errorf("%w: %T is nil", errInvalidRequest, request)
		}

		return typed.settings(), nil
	case SuggestRequest:
		return typed.settings(), nil
	case *SuggestRequest:
		if typed == nil {
			return settings{}, fmt.Errorf("%w: %T is nil", errInvalidRequest, request)
		}

		return typed.settings(), nil
	case CharacteriseRequest:
		return typed.settings(), nil
	case *CharacteriseRequest:
		if typed == nil {
			return settings{}, fmt.Errorf("%w: %T is nil", errInvalidRequest, request)
		}

		return typed.settings(), nil
	case TodosRequest:
		return typed.settings(), nil
	case *TodosRequest:
		if typed == nil {
			return settings{}, fmt.Errorf("%w: %T is nil", errInvalidRequest, request)
		}

		return typed.settings(), nil
	case CurateRequest:
		return typed.settings(), nil
	case *CurateRequest:
		if typed == nil {
			return settings{}, fmt.Errorf("%w: %T is nil", errInvalidRequest, request)
		}

		return typed.settings(), nil
	default:
		return settings{}, fmt.Errorf("%w: unsupported type %T", errInvalidRequest, request)
	}
}

func (r RunRequest) settings() settings {
	s := commonConfig(r.Common)
	s = populationConfig(s, r.Population)
	s = gateConfig(s, r.Gate)
	s.NoCache = r.NoCache

	return s
}

func (r PreviewRequest) settings() settings {
	s := commonConfig(r.Common)
	s = populationConfig(s, r.Population)
	s.mode = previewMode

	return s
}

func (r SuggestRequest) settings() settings {
	s := commonConfig(r.Common)
	s = populationConfig(s, r.Population)
	s = gateConfig(s, r.Gate)
	s.NoCache = r.NoCache
	s.mode = suggestMode
	s.SuggestDryRun = r.DryRun
	s.SurvivorIDs = r.SurvivorIDs
	s.Apply = r.Apply
	s.ApplyAll = r.ApplyAll

	return s
}

func (r CharacteriseRequest) settings() settings {
	s := commonConfig(r.Common)
	s.mode = characteriseMode
	s.PinRung = r.PinRung
	s.CharacteriseWrite = r.Write
	s.CharacteriseForce = r.Force
	s.UntilDry = r.UntilDry
	s.Resume = r.Resume
	s.Answers = r.Answers

	return s
}

func (r TodosRequest) settings() settings {
	s := commonConfig(r.Common)
	s.mode = todosMode
	s.PinRung = r.PinRung
	s.Answers = r.Answers
	s.Resume = r.Resume

	return s
}

func (r CurateRequest) settings() settings {
	s := commonConfig(r.Common)
	s = gateConfig(s, r.Gate)
	s.NoCache = r.NoCache
	s.mode = curateMode

	return s
}

func commonConfig(c Common) settings {
	s := settings{}
	s.ModuleDir = c.ModuleDir
	s.TestDirectory = c.TestDirectory
	s.Jobs = c.Jobs
	s.TimeoutFactor = c.TimeoutFactor
	s.TimeoutFloor = c.TimeoutFloor
	s.AllowRealInfrastructure = c.AllowRealInfrastructure
	s.AllowUnsandboxedEffects = c.AllowUnsandboxedEffects
	s.FailureTripThreshold = c.FailureTripThreshold
	s.TerraformBinary = c.TerraformBinary
	s.Env = c.Env
	s.WorkDir = c.WorkDir
	s.ToolVersion = c.ToolVersion
	s.SetFlags = c.SetFlags
	s.CPUProfile = c.CPUProfile
	s.OTLPEndpoint = c.OTLPEndpoint
	s.Progress = c.Progress
	s.ProgressIntervalForTesting = c.ProgressIntervalForTesting

	return s
}

func populationConfig(s settings, p Population) settings {
	s.TestSelection = p.TestSelection
	s.Tier = p.Tier
	s.IncludeOperators = p.IncludeOperators
	s.ExcludeOperators = p.ExcludeOperators
	s.ExcludePaths = p.ExcludePaths
	s.ExcludeResources = p.ExcludeResources
	s.Since = p.Since
	s.SamplePercent = p.SamplePercent
	s.HasSample = p.HasSample
	s.SampleSeed = p.SampleSeed
	s.GeneratedFunctions = p.GeneratedFunctions
	s.Packs = p.Packs

	return s
}

func gateConfig(s settings, g Gate) settings {
	s.MinScore = g.MinScore
	s.HasMinScore = g.HasMinScore
	s.AllowIncompleteScore = g.AllowIncompleteScore
	s.AllowSampledGate = g.AllowSampledGate
	s.FailOnNew = g.FailOnNew
	s.WriteBaseline = g.WriteBaseline
	s.BaselinePath = g.BaselinePath

	return s
}
