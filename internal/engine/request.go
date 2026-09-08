package engine

import (
	"errors"
	"fmt"
	"time"

	"github.com/andrewesweet/tf-mut/internal/mutation"
)

// Request is the closed set of commands the engine performs. The unexported
// method keeps the set closed: no package outside engine can add a command.
type Request interface{ isRequest() }

var errInvalidRequest = errors.New("invalid engine request")

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
}

func (Config) isRequest()              {}
func (RunRequest) isRequest()          {}
func (PreviewRequest) isRequest()      {}
func (SuggestRequest) isRequest()      {}
func (CharacteriseRequest) isRequest() {}
func (TodosRequest) isRequest()        {}
func (CurateRequest) isRequest()       {}

func configFor(request Request) (Config, error) {
	switch typed := request.(type) {
	case nil:
		return Config{}, fmt.Errorf("%w: request must not be nil", errInvalidRequest)
	case Config:
		return typed, nil
	case *Config:
		if typed == nil {
			return Config{}, fmt.Errorf("%w: %T is nil", errInvalidRequest, request)
		}

		return *typed, nil
	case RunRequest:
		return typed.config(), nil
	case *RunRequest:
		if typed == nil {
			return Config{}, fmt.Errorf("%w: %T is nil", errInvalidRequest, request)
		}

		return typed.config(), nil
	case PreviewRequest:
		return typed.config(), nil
	case *PreviewRequest:
		if typed == nil {
			return Config{}, fmt.Errorf("%w: %T is nil", errInvalidRequest, request)
		}

		return typed.config(), nil
	case SuggestRequest:
		return typed.config(), nil
	case *SuggestRequest:
		if typed == nil {
			return Config{}, fmt.Errorf("%w: %T is nil", errInvalidRequest, request)
		}

		return typed.config(), nil
	case CharacteriseRequest:
		return typed.config(), nil
	case *CharacteriseRequest:
		if typed == nil {
			return Config{}, fmt.Errorf("%w: %T is nil", errInvalidRequest, request)
		}

		return typed.config(), nil
	case TodosRequest:
		return typed.config(), nil
	case *TodosRequest:
		if typed == nil {
			return Config{}, fmt.Errorf("%w: %T is nil", errInvalidRequest, request)
		}

		return typed.config(), nil
	case CurateRequest:
		return typed.config(), nil
	case *CurateRequest:
		if typed == nil {
			return Config{}, fmt.Errorf("%w: %T is nil", errInvalidRequest, request)
		}

		return typed.config(), nil
	default:
		return Config{}, fmt.Errorf("%w: unsupported type %T", errInvalidRequest, request)
	}
}

func (r RunRequest) config() Config {
	settings := commonConfig(r.Common)
	settings = populationConfig(settings, r.Population)
	settings = gateConfig(settings, r.Gate)
	settings.NoCache = r.NoCache
	return settings
}

func (r PreviewRequest) config() Config {
	settings := commonConfig(r.Common)
	settings = populationConfig(settings, r.Population)
	settings.Preview = true
	return settings
}

func (r SuggestRequest) config() Config {
	settings := commonConfig(r.Common)
	settings = populationConfig(settings, r.Population)
	settings = gateConfig(settings, r.Gate)
	settings.NoCache = r.NoCache
	settings.Suggest = true
	settings.SuggestDryRun = r.DryRun
	settings.SurvivorIDs = r.SurvivorIDs
	settings.Apply = r.Apply
	settings.ApplyAll = r.ApplyAll
	return settings
}

func (r CharacteriseRequest) config() Config {
	settings := commonConfig(r.Common)
	settings.Characterise = true
	settings.PinRung = r.PinRung
	settings.CharacteriseWrite = r.Write
	settings.CharacteriseForce = r.Force
	settings.UntilDry = r.UntilDry
	settings.Resume = r.Resume
	settings.Answers = r.Answers
	return settings
}

func (r TodosRequest) config() Config {
	settings := commonConfig(r.Common)
	settings.Todos = true
	settings.PinRung = r.PinRung
	settings.Answers = r.Answers
	settings.Resume = r.Resume
	return settings
}

func (r CurateRequest) config() Config {
	settings := commonConfig(r.Common)
	settings = gateConfig(settings, r.Gate)
	settings.Curate = true
	return settings
}

func commonConfig(c Common) Config {
	settings := Config{}
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

func populationConfig(settings Config, p Population) Config {
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
	return settings
}

func gateConfig(settings Config, g Gate) Config {
	settings.MinScore = g.MinScore
	settings.HasMinScore = g.HasMinScore
	settings.AllowIncompleteScore = g.AllowIncompleteScore
	settings.AllowSampledGate = g.AllowSampledGate
	settings.FailOnNew = g.FailOnNew
	settings.WriteBaseline = g.WriteBaseline
	settings.BaselinePath = g.BaselinePath
	return settings
}
