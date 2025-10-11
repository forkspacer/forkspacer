package version

import "github.com/Masterminds/semver/v3"

var (
	Version       = "unknown" // Set via ldflags
	VersionParsed = semver.MustParse(Version)
	GitCommit     = "unknown" // Set via ldflags
	BuildDate     = "unknown" // Set via ldflags
)
