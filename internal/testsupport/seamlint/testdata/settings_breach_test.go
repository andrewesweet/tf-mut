package engine

// constructSettings deliberately constructs the engine's internal settings
// value at three sites. It is the seam lint's breaching fixture: the scanner
// must flag both composite literals and the new call,
// because a test that builds the settings value is building the input to Run
// instead of the request the seam hands it.
func constructSettings() {
	literal := settings{ModuleDir: "fixture"}
	declared := settings{}
	heap := new(settings)

	literal.Jobs = 1
	declared.Jobs = 1
	heap.Jobs = 1
}
