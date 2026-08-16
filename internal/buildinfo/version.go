// Package buildinfo normalizes build metadata injected by the linker.
package buildinfo

// DevelopmentVersion is reported when no linker version was supplied.
const DevelopmentVersion = "dev"

// Resolve returns a stable display value for linker-provided build metadata.
func Resolve(version string) string {
	if version == "" {
		return DevelopmentVersion
	}

	return version
}
