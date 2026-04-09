package dockerassets

import "path/filepath"

const (
	// DefaultImage is the shipped multi-language worker image tag.
	DefaultImage = "axiom-meeseeks-multi:latest"

	// DefaultDockerfileRelPath is the canonical Dockerfile path used by both
	// source checkouts and release bundles.
	DefaultDockerfileRelPath = "docker/meeseeks-multi.Dockerfile"

	// DefaultBuildContextRelPath is the self-contained Docker build context.
	DefaultBuildContextRelPath = "docker"

	// DefaultBuildCommand is the documented command for preparing the shipped
	// default worker image from a local checkout or release bundle.
	DefaultBuildCommand = "docker build -t axiom-meeseeks-multi:latest -f docker/meeseeks-multi.Dockerfile docker"
)

// DefaultDockerfilePath resolves the canonical Dockerfile path from a root.
func DefaultDockerfilePath(root string) string {
	return filepath.Join(root, filepath.FromSlash(DefaultDockerfileRelPath))
}
