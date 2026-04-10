package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"sync"
)

// Set via ldflags at build time.
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

// populateOnce is a pointer so tests can swap in a fresh guard without
// triggering the vet copylocks check (sync.Once embeds sync.noCopy).
var populateOnce = &sync.Once{}

// populateFromBuildInfo fills in Version/GitCommit/BuildDate from
// runtime/debug.ReadBuildInfo() when the ldflag-set values are still
// the compile-time defaults. This ensures `go install ./cmd/axiom`
// produces useful version output even though the Makefile isn't used.
func populateFromBuildInfo() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}

	if Version == "dev" {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			Version = v
		}
	}

	var (
		revision string
		modified bool
		vcsTime  string
	)
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		case "vcs.time":
			vcsTime = s.Value
		}
	}

	if GitCommit == "unknown" && revision != "" {
		commit := revision
		if len(commit) > 12 {
			commit = commit[:12]
		}
		if modified {
			commit += "-dirty"
		}
		GitCommit = commit
	}

	if BuildDate == "unknown" && vcsTime != "" {
		BuildDate = vcsTime
	}
}

func String() string {
	populateOnce.Do(populateFromBuildInfo)
	return fmt.Sprintf("axiom %s (%s) built %s %s/%s",
		Version, GitCommit, BuildDate, runtime.GOOS, runtime.GOARCH)
}
