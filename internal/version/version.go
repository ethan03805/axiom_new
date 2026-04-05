package version

import (
	"fmt"
	"runtime"
)

// Set via ldflags at build time.
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

func String() string {
	return fmt.Sprintf("axiom %s (%s) built %s %s/%s",
		Version, GitCommit, BuildDate, runtime.GOOS, runtime.GOARCH)
}
