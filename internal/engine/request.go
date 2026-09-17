package engine

import (
	"errors"
	"fmt"
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
	TerraformBinary         string
	Env                     []string
	WorkDir                 string
	ToolVersion             string
	SetFlags                []string
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
func settingsFor(request Request) (config, error) {
	switch typed := request.(type) {
	case nil:
		return config{}, fmt.Errorf("%w: request must not be nil", errInvalidRequest)
	case RunRequest:
		return typed.settings(), nil
	case *RunRequest:
		if typed == nil {
			return config{}, fmt.Errorf("%w: %T is nil", errInvalidRequest, request)
		}

		return typed.settings(), nil
	case PreviewRequest:
		return typed.settings(), nil
	case *PreviewRequest:
		if typed == nil {
			return config{}, fmt.Errorf("%w: %T is nil", errInvalidRequest, request)
		}

		return typed.settings(), nil
	case SuggestRequest:
		return typed.settings(), nil
	case *SuggestRequest:
		if typed == nil {
			return config{}, fmt.Errorf("%w: %T is nil", errInvalidRequest, request)
		}

		return typed.settings(), nil
	case CharacteriseRequest:
		return typed.settings(), nil
	case *CharacteriseRequest:
		if typed == nil {
			return config{}, fmt.Errorf("%w: %T is nil", errInvalidRequest, request)
		}

		return typed.settings(), nil
	case TodosRequest:
		return typed.settings(), nil
	case *TodosRequest:
		if typed == nil {
			return config{}, fmt.Errorf("%w: %T is nil", errInvalidRequest, request)
		}

		return typed.settings(), nil
	case CurateRequest:
		return typed.settings(), nil
	case *CurateRequest:
		if typed == nil {
			return config{}, fmt.Errorf("%w: %T is nil", errInvalidRequest, request)
		}

		return typed.settings(), nil
	default:
		return config{}, fmt.Errorf("%w: unsupported type %T", errInvalidRequest, request)
	}
}

func (r RunRequest) settings() config {
	settings := commonConfig(r.Common)
	settings = populationConfig(settings, r.Population)
	settings = gateConfig(settings, r.Gate)
	settings.NoCache = r.NoCache

	return settings
}

func (r PreviewRequest) settings() config {
	settings := commonConfig(r.Common)
	settings = populationConfig(settings, r.Population)
	settings.mode = previewMode

	return settings
}

func (r SuggestRequest) settings() config {
	settings := commonConfig(r.Common)
	settings = populationConfig(settings, r.Population)
	settings = gateConfig(settings, r.Gate)
	settings.NoCache = r.NoCache
	settings.mode = suggestMode
	settings.SuggestDryRun = r.DryRun
	settings.SurvivorIDs = r.SurvivorIDs
	settings.Apply = r.Apply
	settings.ApplyAll = r.ApplyAll

	return settings
}

func (r CharacteriseRequest) settings() config {
	settings := commonConfig(r.Common)
	settings.mode = characteriseMode
	settings.PinRung = r.PinRung
	settings.CharacteriseWrite = r.Write
	settings.CharacteriseForce = r.Force
	settings.UntilDry = r.UntilDry
	settings.Resume = r.Resume
	settings.Answers = r.Answers

	return settings
}

func (r TodosRequest) settings() config {
	settings := commonConfig(r.Common)
	settings.mode = todosMode
	settings.PinRung = r.PinRung
	settings.Answers = r.Answers
	settings.Resume = r.Resume

	return settings
}

func (r CurateRequest) settings() config {
	settings := commonConfig(r.Common)
	settings = gateConfig(settings, r.Gate)
	settings.NoCache = r.NoCache
	settings.mode = curateMode

	return settings
}

func commonConfig(c Common) config {
	settings := config{}
	settings.ModuleDir = c.ModuleDir
	settings.TestDirectory = c.TestDirectory
	settings.Jobs = c.Jobs
	settings.TimeoutFactor = c.TimeoutFactor
	settings.TimeoutFloor = c.TimeoutFloor
	settings.AllowRealInfrastructure = c.AllowRealInfrastructure
	settings.AllowUnsandboxedEffects = c.AllowUnsandboxedEffects
	settings.TerraformBinary = c.TerraformBinary
	settings.Env = c.Env
	settings.WorkDir = c.WorkDir
	settings.ToolVersion = c.ToolVersion
	settings.SetFlags = c.SetFlags

	return settings
}

func populationConfig(settings config, p Population) config {
	settings.TestSelection = p.TestSelection
	settings.Tier = p.Tier
	settings.IncludeOperators = p.IncludeOperators
	settings.ExcludeOperators = p.ExcludeOperators
	settings.ExcludePaths = p.ExcludePaths
	settings.ExcludeResources = p.ExcludeResources
	settings.Since = p.Since
	settings.SamplePercent = p.SamplePercent
	settings.HasSample = p.HasSample
	settings.SampleSeed = p.SampleSeed
	settings.GeneratedFunctions = p.GeneratedFunctions
	settings.Packs = p.Packs

	return settings
}

func gateConfig(settings config, g Gate) config {
	settings.MinScore = g.MinScore
	settings.HasMinScore = g.HasMinScore
	settings.AllowIncompleteScore = g.AllowIncompleteScore
	settings.AllowSampledGate = g.AllowSampledGate
	settings.FailOnNew = g.FailOnNew
	settings.WriteBaseline = g.WriteBaseline
	settings.BaselinePath = g.BaselinePath

	return settings
}
