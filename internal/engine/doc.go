// Package engine is the single seam of the tool: configuration in, report out.
//
// Everything the command line does is a thin shell over Run, and every test of
// product behaviour drives this entry point against the real Terraform binary.
//
// The rest of this comment maps the application layer by pipeline stage, and
// the audit beside TestExportedEngineTypesDeclareNoSeamControls fails when a
// non-test file in the package — this one, doc.go, included — is not named in
// it, so the map moves with the files. The audit itself lives beside the
// other document audits in audit_test.go.
//
// # Composition root
//
// dependencies.go declares Dependencies — the runner template, checkout
// resolver, tracer factory and warning writer — with DefaultDependencies,
// New and Engine. Run (engine.go) is the default wiring over them and
// remains the seam.
//
// # Requests and settings
//
// request.go declares the closed request set — RunRequest, PreviewRequest,
// SuggestRequest, CharacteriseRequest, TodosRequest and CurateRequest,
// closed by the unexported isRequest method, the request's type being the
// command — and settingsFor, the one route from a request to the internal
// settings value. The settings type itself lives in engine.go; its defaults,
// caps and population helpers live in settings.go, with the configuration
// merge and the suppression policy beside them in policy.go.
//
// # The grading pipeline, in order
//
// Run (engine.go) obtains settings, starts instrumentation and delegates to
// dispatch. Its request type switch answers todos locally; other requests
// reach the version check and discovery through probe.
// The grading commands share mutate (engine.go), whose stages are owned
// file by file; preview stops after the count levers:
//
//   - The safety gates, decided statically before init, schema retrieval
//     or test execution, after the version probe:
//     safety.go refuses real infrastructure and unsandboxed effects;
//     preview carries refusals as warnings. jsonfloor.go withdraws the
//     static shortcuts while an unread JSON file in the closure leaves
//     the run blind.
//
//   - Warm-up: baseline.go materialises and initialises the sandbox, then
//     reads the provider schemas (prepare, warmWorkspace); backend.go
//     neutralises backend blocks in the staged copy before init. Except
//     in preview mode, the suite then runs twice, verbose: the first run
//     proves it green, and both supply the fingerprints for the dynamic
//     mask. mask.go's staticMask folds the static volatility scan into
//     that mask.
//
//   - Generation: engine.go's build drives mutation.Generator; packs.go
//     has resolved and loaded the packs the generator's selection
//     consumes.
//
//   - Description and suppression: projection.go's describeMutants gives
//     each mutant its published identity; policy.go's suppress moves the
//     configured exclusions and inline directives to Ignored.
//
//   - The count levers: since.go scopes the population by --since (and
//     finds the checkout root); engine.go's applyCountLevers adds
//     --sample.
//
//   - Execution: execute.go owns the plan and the workers (execute,
//     executionPlan) and the consecutive-failure trip; oracle.go's
//     executionOracle gathers phase-two evidence for phase-one survivors
//     only; engine.go's executeWithCache replays and refreshes the
//     verdict cache around them.
//
//   - Population aggregates: engine.go's classifyRun and finishReport
//     publish the population fact over the dispatcher events.go declares;
//     projection.go's gradingProjection.completeReport computes the
//     aggregates first. findings.go derives the headline (resources
//     planned and asserted on by nothing); metrics.go projects the
//     population arithmetic.
//
//   - The suggest leg (suggest mode): suggest.go generates candidates for
//     the survivors; unless this is a dry run, verify.go proves each
//     candidate against the baseline and against its own mutant.
//
//   - Acceptance: acceptance.go applies the baseline gate table after the
//     suggestion leg succeeds, so a stale survivor selection fails before
//     the baseline writer runs.
//
//   - Final report: projection.go's gradingProjection.project attaches
//     the aggregates, suggestions and acceptance labels. The package-level
//     project maps individual oracle outcomes earlier in the pipeline.
//
//   - Apply (suggest mode): apply.go writes the verified suggestions under
//     the snapshot-bound preflight-then-atomic-rename protocol
//     (applySuggestions).
//
// # The other commands
//
// characterise.go scaffolds, harvests and pins a first suite
// (characteriseModule); untildry.go is its close-the-gap loop, pinning
// assertion kills only, under the granularity ladder; characterisewrite.go
// is the write protocol that places the generated suite and its provenance
// registry; todos.go lists and resolves judgement points without running
// Terraform; curate.go grades a full population and reports redundancy.
//
// # Persistence
//
// Three files own the on-disk formats: cache.go the verdict cache under
// cacheFormatVersion; acceptance.go the acceptance list under
// baselineFormatVersion; characterisewrite.go the generated-suite registry
// under registryVersion.
//
// # Projections
//
// projection.go projects the grading outcome; metrics.go the population
// arithmetic; characteriseprojection.go the characterisation context's
// status and provenance vocabularies; rules.go renders the operator
// catalogue for the reporters (RuleDescriptions).
//
// # Run infrastructure
//
// progress.go writes the periodic long-run line; profile.go is the
// command's own CPU profile; telemetry.go names the phases and spans;
// events.go declares the engine-owned domain events.
//
// # Seam controls
//
// Most test seams are package-level variables with production defaults:
// disableStaticShortcuts, disableJSONReading and
// seedGenerationDefect in engine.go; progressInterval in progress.go;
// seedSuggestionDefect in suggest.go; gitTimeout in since.go;
// applyCommitProbe in apply.go; seedMissingMock, seedNoEscalation and
// seedSharedFileOrder in characterise.go; seedClosureChange,
// seedClosureFile, seedClosureAfter, seedRenameWindowChange and
// seedRegistryFailure in characterisewrite.go; seedUntilDryRounds in
// untildry.go; pinDefectHooks rides the CharacteriseRequest in request.go.
package engine
