set export
set minimum-version := "1.58.0"
set shell := ["bash", "-euo", "pipefail", "-c"]

repo_root := justfile_directory()
artifact_dir := repo_root / ".artifacts"
binary := artifact_dir / "bin/tf-mut"
version := `git describe --always --dirty`

export GOTOOLCHAIN := "local"
export GOCACHE := artifact_dir / "cache/go-build"
export GOMODCACHE := repo_root / ".tools/cache/go-mod"
export GOLANGCI_LINT_CACHE := artifact_dir / "cache/golangci-lint"
export MISE_CACHE_DIR := artifact_dir / "cache/mise"
export MISE_CONFIG_DIR := artifact_dir / "mise-config"
export XDG_CACHE_HOME := artifact_dir / "cache/xdg"
export PATH := repo_root + "/.tools/bin:" + env_var("PATH")

# List public build-chain commands.
default:
    @just --list

# Verify platform, exact tools, locks, provider mirror, compiler, and agent adapters.
doctor:
    mise exec -- scripts/doctor

# Install the exact locked prebuilt, source-built, and Terraform provider tools.
tools-install:
    mise install --yes
    mise exec -- scripts/install-source-tools
    mise exec -- scripts/install-providers
    mise exec -- scripts/doctor

# Report available prebuilt and source-tool updates without changing files.
tools-outdated:
    mise outdated --local --bump
    mise exec -- bash -c 'for module in tools/gopls tools/goimports tools/govulncheck; do (cd "$module" && go list -m -u all); done'
    @echo "Pinned providers:"
    @cat tools/providers.allowlist

# Resolve current prebuilt and source-tool releases, reinstall, and run the gate.
tools-update:
    MISE_SAFE=1 mise upgrade --bump --yes
    MISE_SAFE=1 mise lock --platform linux-x64 --yes
    mise exec -- go run ./internal/buildchain/cmd sync-go-toolchains
    mise exec -- scripts/update-source-tools
    mise exec -- just ci

# Update the exact null-provider constraint, checksums, allowlist, and mirror.
providers-update version:
    mise exec -- scripts/update-provider "{{ version }}"
    mise exec -- just test

# Update product/test Go dependencies and run the full pull-request gate.
deps-update:
    mise exec -- go get -u all
    mise exec -- go mod tidy
    mise exec -- just ci

# Deliberately update tools, one reviewed provider version, and dependencies.
update provider_version:
    mise exec -- just tools-update
    mise exec -- just providers-update "{{ provider_version }}"
    mise exec -- just deps-update

# Build the reproducible Linux executable.
build:
    mkdir -p "{{ artifact_dir }}/bin"
    mise exec -- env CGO_ENABLED=0 go build -trimpath -buildvcs=true -mod=readonly \
      -ldflags "-X main.version={{ version }}" -o "{{ binary }}" ./cmd/tf-mut

# Apply canonical Go, Bash, JSON, YAML, TOML, and Terraform formatting.
fmt:
    mise exec -- scripts/format write

# Check canonical formatting without modifying the source tree.
fmt-check:
    mise exec -- scripts/format check

# Run every read-only language and workflow quality gate.
lint: lint-go lint-shell lint-config lint-actions

# Run the pinned aggressive Go linters without fixes, over both build tags.
lint-go:
    mise exec -- golangci-lint run ./...
    mise exec -- golangci-lint run --build-tags=integration ./...

# Parse and lint every declared Bash script with bash -n, shfmt, and shellcheck.
lint-shell:
    mise exec -- scripts/lint-shell

# Parse, format-check, and lint every declared JSON, YAML, and TOML file.
lint-config:
    mise exec -- scripts/lint-config

# Parse, lint, and aggressively security-audit GitHub Actions with actionlint and zizmor.
lint-actions:
    mise exec -- scripts/lint-actions

# Apply safe lint fixes twice and require convergence.
lint-fix:
    mise exec -- scripts/lint-fix

# Apply all canonical source fixes.
fix: lint-fix fmt

# Compile and vet production and test packages without executing tests.
typecheck:
    mise exec -- go build -mod=readonly ./...
    mise exec -- go vet -tests=true ./...
    mise exec -- go build -mod=readonly -tags=integration ./...
    mise exec -- go vet -tests=true -tags=integration ./...

# Verify root and source-tool module integrity and tidiness.
mod-check:
    mise exec -- scripts/mod-check

# Run the M2a honesty gate: the reproductions the oracle has to survive.
gate:
    mkdir -p "{{ artifact_dir }}/test"
    mise exec -- gotestsum --format testname --junitfile "{{ artifact_dir }}/test/gate.xml" \
      --raw-command -- go test ./internal/engine/ ./internal/fingerprint/ ./internal/tfexec/ \
      -json -count=1 -run '^(TestUnknownRefinementSurvivesRatherThanBeingExcluded|TestAKnownPayloadLetsTheOracleProveUnobservability|TestAVolatileTemplateStillYieldsAFindingOnItsStableSuffix|TestADeterministicIdentifierIsNeverMasked|TestVolatilityFixturesClassifyIdenticallyAcrossRepeatedRuns|TestVolatilityIntroducedOnlyByTheMutantIsDecidedByARerun|TestStateAndDiagnosisAreIndependentOfOrderAndParallelism|TestADeltaSeenOnlyThroughALocalAndAnOutputIsNotNoAssertion|TestADefeatedClosureDiagnosesUnassertedAndNamesTheConstruct|TestConstructsWithNoProjectionAreStructurallyUnassertable|TestTheHigherDiagnosisWinsWhenTwoPredicatesHold|TestPhaseTwoRunsOnlyForPhaseOneSurvivors|TestAPayloadOutsideThePinnedVersionRangeIsAnOperationalFailure|TestVerboseDecodeOfALargeStreamStaysWithinTheHeapCeiling|TestTruncatedStreamIsAnErrorRatherThanAnEmptyResult|TestMalformedStreamIsAnErrorRatherThanAnEmptyResult)$'


# Run the M3 offline gates: graph soundness, the count levers, the gate table.
gate-m3:
    mkdir -p "{{ artifact_dir }}/test"
    mise exec -- gotestsum --format testname --junitfile "{{ artifact_dir }}/test/gate-m3.xml" \
      --raw-command -- go test ./internal/engine/ \
      -json -count=1 -run '^(TestEveryGenerationSiteMapsIntoTheGraph|TestAnUnmappableSiteFallsBackClosed|TestAnUnmappablePayloadUnknownIsInCone|TestTheForwardConeUnionsSameResourceAttributes|TestGraphAgreesWithTerraformGraphOverTheCorpus|TestASeededMissingEdgeFailsTheSupplementalCheck|TestNoUserInvocationExecutesTerraformGraph|TestAnOutOfConeUnknownPermitsPlanModeUnobservable|TestOwnResourceUnknownsStayInCone|TestStaticUnobservableEqualsTheExecutedVerdict|TestTheContractFixtureClassifiesIdenticallyUnderTheShortcut|TestABodyOnlyMutantOfAnUninstantiatedBlockStaysNoCoverage|TestAMutantInsideTheConditionExecutes|TestAMutantUpstreamOfTheConditionExecutes|TestExcludedCategoriesFailClosedToExecution|TestDecidableToNonzeroControlsClassifyByExecution|TestAnUncommittedNewResourceIsSelected|TestStagedAndUnstagedChangesAreSelected|TestACommittedRangeIsSelected|TestARenameFollowsBothNames|TestADeletionSelectsTheWholeModule|TestAMissingRefIsAnError|TestOutsideARepositoryIsAnError|TestAMergeConflictIsAnError|TestAShallowCloneLackingTheRefIsAnError|TestNonTerraformClassChangesForceTheFullPopulation|TestSinceSelectionIsDeterministic|TestSamplingIsDeterministicAndNonAuthoritative|TestASampledGateIsRefusedWithoutTheOptIn|TestASecondUnchangedRunIsAllCacheHits|TestCacheInvalidationPerKeyDimension|TestMaskedBaselineFingerprintIsAKeyDimension|TestCacheIsRefusedUnderTheUnsafeOptIns|TestNoCacheDisablesReadsAndWrites|TestCorruptionIsAMiss|TestCacheEntriesAreOwnerOnly|TestASymlinkedCacheDirectoryIsRefused|TestEvictionIsDeterministicAndSizeCapped|TestConcurrentInvocationsShareTheCacheSafely|TestAdoptionThenRegression|TestAcceptedSurvivorsPassAndStayInScores|TestAnAcceptedIndeterminateTurningActionableIsNew|TestStaleAndUnobservedAreDistinguished|TestBaselineWriteIsRefusedOffTheFullPopulation|TestASampledFailOnNewIsRefusedWithoutTheOptIn|TestScopedFailOnNewJudgesTheSelectedPopulationOnly|TestBaselineNeverSuppressesSafetyGates|TestExitCodesAreDeterministicAcrossTheTable|TestVerdictInvarianceUnderSince|TestVerdictInvarianceUnderSampling|TestTheCachedRowOfTheGateTable|TestScopedMinScoreIsLabelledPartial|TestTheLockFileIsAKeyDimension|TestRemoteModulePayloadsAreAKeyDimension|TestAutoVarFilesAreAKeyDimension|TestProviderEnvironmentIsAKeyDimension|TestAnExistingLooseCacheDirectoryIsCorrected|TestAnAutoLoadedVariableFileDefeatsTheDefault|TestTerraformTfvarsDecidesOverTheDefault|TestAnIgnoredUntrackedVariableFileForcesTheFullPopulation|TestAWhitespaceFilenameSurvivesSelection|TestAJSONConfigurationChangeForcesTheFullPopulation|TestAFractionalSampleNeverKeepsNothing|TestProviderConfigurationMakesTheConeUnbounded|TestModuleWiringAgreesWithTerraformGraph)$'

# Run the M4 offline gates: the JSON safety floor, the suggestion-soundness gate, the apply protocol, and the skill contract.
gate-m4:
    mkdir -p "{{ artifact_dir }}/test"
    mise exec -- gotestsum --format testname --junitfile "{{ artifact_dir }}/test/gate-m4.xml" \
      --raw-command -- go test ./internal/engine/ ./internal/suggest/ ./internal/skill/ ./cmd/tf-mut/ \
      -json -count=1 -run '^(TestUnreadJSONFailsTheRealInfrastructureGateClosed|TestUnreadJSONFailsTheUnsandboxedEffectsGateClosed|TestEachSafetyGateFailsClosedIndependentlyOnUnreadJSON|TestMalformedJSONRetainsTheFloorRatherThanLiftingIt|TestConfiguredExclusionsCannotHideUnreadJSON|TestNoTerraformRunPrecedesAFloorRefusal|TestAJSONAutoVariableFileKeepsTheStaticShortcutsDown|TestUnreadJSONDisablesEveryStaticShortcut|TestTheHCLOnlyGraphIsTheFalseProofTheFloorWithdraws|TestAuthorisingBothGatesStillReportsTheUnreadJSON|TestAPreviewIsNeverRefusedByTheFloor|TestNoJSONInTheClosureLeavesTheFloorUp|TestTheSliceFiresTheRealInfrastructureGateFromContent|TestTheSliceFiresTheEffectsGateFromContent|TestTheSliceReadsMockStatusFromAJSONTestFile|TestTheSliceLetsTheEvaluatorReadAJSONVariablesFile|TestTheSliceRepairsTheMixedModuleFalseProof|TestAParseFailureRetainsTheFloorWhileItsNeighboursLiftTheirs|TestWellFormedJSONThisVersionCannotModelRetainsTheFloor|TestNoJSONFileIsEverAMutationSite|TestAJSONPopulationIsUnchangedByReadingIt|TestAChangedJSONConfigurationIsACacheKeyDimension|TestTheAddressAdapterMatrix|TestChildModuleInternalsAreNeverALegalAssertionSurface|TestAGeneratorLimitIsNeverARefutation|TestTheRenderingContractMatrix|TestATosetAmbiguityIsAlwaysSkipped|TestTheSensitivityPredicateRefusesBeforeAnythingRenders|TestSuggestGeneratesTheAssertionThatWouldHaveKilledASurvivor|TestADryRunVerifiesNothing|TestSuggestionIdentifiersAreStableAcrossRunsAndUnrelatedEdits|TestIndeterminateSurvivorsAndUnassertableMutantsReceiveNoSuggestion|TestAJSONTestTargetIsSkippedWithNoPatch|TestASensitiveValueReachesNoSuggestionArtefact|TestEverySkippedStatusCarriesNoPatchAndAReason|TestVerifiedRequiresBothLegsAndCarriesTheirEvidence|TestASeededWrongValueIsRefutedThroughTheBaselineLeg|TestASeededVacuousAssertionIsRefutedThroughTheMutantLeg|TestASuggestExitCodeIsOneOnlyWhenSomethingIsRefuted|TestAStaleSurvivorIdentifierIsAnOperationalFailureNamingIt|TestSurvivorSelectionScopesTheSuggestions|TestVerificationIsNeverCached|TestACleanApplyWritesAtomicallyAndTheMutantsDie|TestAnEditBetweenVerificationAndApplyAbortsWithZeroWrites|TestAStaleVerifiedDigestAbortsThePreflightNamingBothDigests|TestApplyRefusesANonVerifiedSelection|TestApplyRefusesAnUnknownSuggestionIdentifier|TestASymlinkedTargetAbortsBeforeAnyWrite|TestAJSONTestFileIsNeverWrittenByApply|TestAMultiFileApplyReportsAPartialFailureExplicitly|TestApplyIsTheThirdWriteExceptionAndTouchesOnlyItsTargets|TestAFreshInstallPlacesTheSkillAtTheDocumentedPath|TestASameVersionReinstallIsANoOp|TestAUserEditSurvivesAReinstallUnlessForced|TestACrossVersionUpgradeReplacesAnUnmodifiedInstallAndReportsIt|TestTheContentCarriesTheContractedRules|TestTheInstalledSkillReferencesOnlyCommandsAndFlagsTheBinaryHas|TestEveryOutcomeTableRowIsReachableThroughTheSeam|TestAScopedSuggestRunStatesItsVerificationCost|TestAnOutOfClosureTargetAbortsThePreflight|TestAnUnparseableTargetAbortsThePreflight|TestExclusionsCannotHideJSONDeclaredContentUnderTheSlice|TestNoTerraformRunPrecedesAContentDrivenRefusal|TestAJSONDeclaredModuleCallJoinsTheClosure|TestAnUnmodelledJSONRunArgumentRetainsTheFloor|TestAnUnmodelledNestedTerraformConstructRetainsTheFloor|TestSuggestIsWiredThroughTheCommandLine|TestSuggestSurvivorSelectionIsWiredThroughTheCommandLine|TestSuggestApplySelectionIsWiredThroughTheCommandLine|TestTheReportedPatchIsTheBytesApplyWrites|TestAnEditBetweenPreflightAndCommitAbortsTheReplacement|TestAMutantSurfacedSecretReachesNoReport|TestAMovedBlockInJSONIsRefusedByName|TestAJSONMockProviderBodyBeyondAliasRetainsTheFloor|TestArgumentsAfterTheModulePathAreRefused|TestSurvivorsSharingOneAssertionCollapseIntoOneSuggestion|TestADryRunRefusesAnApplySelection|TestSuggestRefusesATestSelection|TestARealSuggestReportValidatesAgainstThePublishedSchema|TestMockDataFilesAreAKeyDimension)$'

# Run the M4.5 offline gates: the #70 collectors, the scaffold-soundness gate, the TODO protocol, until-dry, curate and the end-of-MVP walkthrough.
gate-m45:
    mkdir -p "{{ artifact_dir }}/test"
    mise exec -- gotestsum --format testname --junitfile "{{ artifact_dir }}/test/gate-m45.xml" \
      --raw-command -- go test ./internal/engine/ ./internal/skill/ ./internal/sandbox/ ./cmd/tf-mut/ \
      -json -count=1 -run '^(TestACheckScopedDataSourceReachesTheProviderInventory|TestACheckScopedDataSourceReachesTheEffectInventory|TestARemovedBlockReachesTheProviderInventory|TestARemovedBlockProvisionerReachesTheEffectInventoryAndNeverExecutes|TestACheckScopedDataSourceInJSONReachesTheEffectInventory|TestACheckScopedDataSourceInJSONReachesTheProviderInventory|TestARemovedBlockInJSONReachesTheEffectInventory|TestARemovedBlockInJSONReachesTheProviderInventory|TestAMovedBlockIsRefusedInHCL|TestAnImportBlockIsRefusedInHCL|TestAMovedBlockInJSONIsRefusedByName|TestAnImportBlockInJSONIsRefusedByName|TestAnUntestedAliasedProviderModuleCharacterisesWithNoOptIn|TestAMissingAliasMockRefusesBeforeExecution|TestTheDefaultCharacterisationWritesNothing|TestAWrittenSuiteIsGreenAndRegistered|TestASecondWriteIsRefusedAsACollision|TestForceReplacesOnlyUnmodifiedGeneratedFiles|TestScenariosCarryDistinctStateKeys|TestAZeroOutputModuleEscalatesAndSaysSo|TestARungThatPinsNothingIsNeverComplete|TestTheConfiguredRungPinsOnlyWhatTheConfigurationDetermined|TestAnUnknownRungIsRefused|TestASensitiveValueReachesNoGeneratedArtefact|TestARealCharacterisationReportValidatesAgainstThePublishedSchema|TestBranchExpansionPinsBothSidesOfAConditional|TestScenarioPinsAreInvariantUnderFileOrder|TestUntilDryConvergesWithoutWritingAByte|TestUntilDryRespectsTheGranularityLadder|TestCurateRefusesAPartialPopulationAtConfigurationTime|TestCurateReportsAnEmptyKillSetWithItsEvidence|TestCurateWritesNothing|TestOneMutantFailingTwoAssertionsAttributesBoth|TestUnassertableConstructsBecomeNonExecutableScaffolds|TestAnUnsynthesizableInputBecomesANonExecutableArtefact|TestAnAnsweredTodoIsPromotedAndTheSuiteIsGreen|TestTodosListsTheOpenJudgementPointsWithTheirEvidence|TestASecretInAFailedAttemptReachesNoArtefact|TestCharacteriseIsWiredThroughTheCommandLine|TestCharacteriseRefusesArgumentsAfterTheModulePath|TestTheTodoSurfacesAreWiredInBothArgumentOrders|TestTodosRefusesArgumentsAfterTheModulePath|TestTheInstalledSkillsWalkthroughExecutes|TestASeededWrongFlagInTheSkillTurnsTheGateRed|TestARefutedAnswerIsRejectedRatherThanAnOperationalFailure|TestAClosureChangeAtTheProbeYieldsZeroWrites|TestNoTerraformRunPrecedesAStagedGateRefusal|TestCurateIsWiredThroughTheCommandLine|TestCurateRefusesArgumentsAfterTheModulePath|TestAMinedValidationResolvesAnInputWithNoDefault|TestASensitiveAnswerIsVerifiedAndStillWithheld|TestANewClosureFileAtTheProbeYieldsZeroWrites|TestAPartialCommitReportsWhatItWrote|TestAnAnsweredScaffoldIsVerifiedBeforeItIsPromoted|TestCurateDrawsNoConclusionAboutItsOwnGeneratedAssertions|TestTheFinalPinSetIsVerifiedBeforeAnyWrite|TestEveryTestNameADocumentClaimsExists|TestAPartialSkillInstallReportsWhatLanded|TestAStagedPathCannotEscapeTheSandbox|TestAScaffoldAnswerCannotInjectConfiguration|TestAForeignRegistryIsNeverReplaced|TestARegistryFailureReportsThePartialState|TestAClosureChangeInsideTheRenameWindowIsCaught|TestConfigurationAliasesAreMockedAndGated|TestTheGranularityFlagReachesTheEngine)$'

# Run fixed-seed Go/property/corpus tests and offline real-Terraform fixtures.
test: _test-go _test-terraform

# Run only the fixed-seed Rapid property suite.
test-property:
    mkdir -p "{{ artifact_dir }}/test"
    RAPID_SEED=424242 RAPID_CHECKS=1000 mise exec -- \
      gotestsum --format testname --junitfile "{{ artifact_dir }}/test/property.xml" \
      --raw-command -- go test ./... -json -count=1 \
      -run TestVersionOutputPreservesBuildValue

# Run randomized property tests and record the emitted seeds in test output.
test-random:
    mkdir -p "{{ artifact_dir }}/test"
    seed=$(od -An -N8 -tu8 /dev/urandom | tr -d ' '); \
      printf 'rapid.seed=%s\n' "$seed" | tee "{{ artifact_dir }}/test/random-seed.txt"; \
      RAPID_SEED="$seed" RAPID_CHECKS=1000 mise exec -- \
        gotestsum --format testname --jsonfile "{{ artifact_dir }}/test/random.json" \
        --raw-command -- go test ./... -json -count=1 \
        -shuffle=on

# Run the offline suite with the race detector and checked C toolchain.
test-race:
    mkdir -p "{{ artifact_dir }}/test"
    mise exec -- env CGO_ENABLED=1 RAPID_SEED=424242 gotestsum --format testname \
      --junitfile "{{ artifact_dir }}/test/race.xml" --raw-command -- \
      go test ./... -json -count=1 -race -shuffle=424242

# Run opt-in integration-tag tests that may use credentials or real providers.
test-integration:
    test "${TF_MUT_ALLOW_REAL_INFRASTRUCTURE:-}" = "1"
    mise exec -- gotestsum --format testname --raw-command -- \
      go test ./... -json -count=1 -tags=integration

# Measure the synthesis rate over the pinned public-module corpus (M4.5-0).
measure-synthesis:
    test "${TF_MUT_ALLOW_REAL_INFRASTRUCTURE:-}" = "1"
    mkdir -p "{{ artifact_dir }}/measurement"
    mise exec -- go test -tags=integration ./internal/engine/ -count=1 -v \
      -run '^TestTheSynthesisRateOverThePinnedCorpus$'

# Run opt-in realistically sized performance benchmarks.
test-performance:
    test "${TF_MUT_ALLOW_REAL_INFRASTRUCTURE:-}" = "1"
    mkdir -p "{{ artifact_dir }}/performance"
    mise exec -- go test -run '^$' -bench Performance -tags=integration ./... \
      >"{{ artifact_dir }}/performance/bench.txt"

# Fuzz one explicit package/target for a bounded duration.
fuzz package target duration="10s":
    mise exec -- go test "{{ package }}" -run '^$' -fuzz '^{{ target }}$' -fuzztime "{{ duration }}"

# Discover every native fuzz target, fail if none exist, and run each with a bound.
fuzz-all duration="10s":
    mise exec -- scripts/fuzz-all "{{ duration }}"

# Run cross-package diff mutation with a report-only ten-minute budget.
mutate-diff base:
    mise exec -- scripts/mutate-diff "{{ base }}"

# Run a full cross-package mutation sweep.
mutate:
    mkdir -p "{{ artifact_dir }}/mutation"
    mise exec -- scripts/run-gremlins \
      --output "{{ artifact_dir }}/mutation/full.json"
    mise exec -- jq -e \
      '[.files[] | select(.file_name == "internal/buildinfo/version.go") | .mutations[]] \
       | length > 0 and all(.status == "KILLED")' \
      "{{ artifact_dir }}/mutation/full.json" >/dev/null

# Run live vulnerability, dependency, provider, and redacted secret scans.
security:
    mkdir -p "{{ artifact_dir }}/security"
    mise exec -- zizmor --offline --persona=auditor --strict-collection --format=sarif . \
      >"{{ artifact_dir }}/security/zizmor.sarif"
    mise exec -- scripts/lint-actions
    mise exec -- scripts/mod-check
    mise exec -- govulncheck -format=json -test ./... \
      >"{{ artifact_dir }}/security/govulncheck.json"
    mise exec -- govulncheck -test ./...
    mise exec -- govulncheck -format=json -mode=binary .tools/bin/gopls \
      >"{{ artifact_dir }}/security/gopls-govulncheck.json"
    mise exec -- govulncheck -mode=binary .tools/bin/gopls
    mise exec -- osv-scanner scan source --recursive --call-analysis=go \
      --experimental-exclude tools/gopls \
      --format=json --output-file "{{ artifact_dir }}/security/osv.json" .
    mise exec -- osv-scanner scan source --recursive --call-analysis=go \
      --experimental-exclude tools/gopls .
    mise exec -- gitleaks dir --redact --no-banner --report-format json \
      --report-path "{{ artifact_dir }}/security/gitleaks.json" .
    mise exec -- gitleaks git --redact --no-banner --log-opts=--all \
      --report-format json \
      --report-path "{{ artifact_dir }}/security/gitleaks-history.json"
    mise exec -- golangci-lint run ./...
    mise exec -- scripts/doctor

# Verify shared instructions, links, adapters, and the one-way permission invariant.
agent-check:
    mise exec -- scripts/check-agent-parity

# Run the hermetic, source-tree-clean pull-request gate.
ci:
    mise exec -- scripts/ci

# Run scheduled race, random property, fuzz, and full mutation gates.
ci-full: ci test-race test-random fuzz-all mutate

_test-go:
    mkdir -p "{{ artifact_dir }}/test"
    RAPID_SEED=424242 RAPID_CHECKS=100 mise exec -- \
      gotestsum --format testname --junitfile "{{ artifact_dir }}/test/unit.xml" \
      --jsonfile "{{ artifact_dir }}/test/events.json" --raw-command -- \
      go test ./... -json -count=1 -shuffle=424242

_test-terraform:
    mise exec -- tests/build-chain/terraform-offline.sh

# Package the built binaries as versioned, checksummed per-architecture assets.
package-test-release version out_dir:
    mkdir -p "{{ out_dir }}/{{ version }}"
    mise exec -- env CGO_ENABLED=0 GOARCH=amd64 go build -trimpath -mod=readonly -o "{{ out_dir }}/amd64/tf-mut" ./cmd/tf-mut
    mise exec -- env CGO_ENABLED=0 GOARCH=arm64 go build -trimpath -mod=readonly -o "{{ out_dir }}/arm64/tf-mut" ./cmd/tf-mut
    tar -czf "{{ out_dir }}/{{ version }}/tf-mut_{{ version }}_linux_amd64.tar.gz" -C "{{ out_dir }}/amd64" tf-mut
    tar -czf "{{ out_dir }}/{{ version }}/tf-mut_{{ version }}_linux_arm64.tar.gz" -C "{{ out_dir }}/arm64" tf-mut
    cd "{{ out_dir }}/{{ version }}" && sha256sum -- *.tar.gz > checksums.txt
