package workflow_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestReleaseArtifactBuildsStampModuleVersion(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	expectedTarget := modulePath(t, repositoryRoot) + "/common.Version"
	releaseWorkflow := loadArtifactWorkflow(t, filepath.Join(repositoryRoot, ".github", "workflows", "release.yml"))
	electronWorkflow := loadArtifactWorkflow(t, filepath.Join(repositoryRoot, ".github", "workflows", "electron-build.yml"))

	artifactBuilds := []struct {
		name       string
		script     string
		assignment string
	}{
		{
			name:       "Linux amd64 release",
			script:     namedStep(t, artifactJobByName(t, releaseWorkflow, "linux"), "Build Backend (amd64)").Run,
			assignment: "-X=" + expectedTarget + "=${VERSION}",
		},
		{
			name:       "Linux arm64 release",
			script:     namedStep(t, artifactJobByName(t, releaseWorkflow, "linux"), "Build Backend (arm64)").Run,
			assignment: "-X=" + expectedTarget + "=${VERSION}",
		},
		{
			name:       "macOS release",
			script:     namedStep(t, artifactJobByName(t, releaseWorkflow, "macos"), "Build Backend").Run,
			assignment: "-X=" + expectedTarget + "=${VERSION}",
		},
		{
			name:       "Windows release",
			script:     namedStep(t, artifactJobByName(t, releaseWorkflow, "windows"), "Build Backend").Run,
			assignment: "-X=" + expectedTarget + "=${VERSION}",
		},
		{
			name:       "Electron Windows release",
			script:     namedStep(t, artifactJobByName(t, electronWorkflow, "build"), "Build Go binary (Windows)").Run,
			assignment: "-X=" + expectedTarget + "=${VERSION}",
		},
		{
			name:       "container release",
			script:     string(readRepositoryFile(t, filepath.Join(repositoryRoot, "Dockerfile"))),
			assignment: "-X=" + expectedTarget + "=${VERSION}",
		},
		{
			name:       "development container",
			script:     string(readRepositoryFile(t, filepath.Join(repositoryRoot, "Dockerfile.dev"))),
			assignment: "-X '" + expectedTarget + "=$(cat VERSION)'",
		},
	}

	for _, artifact := range artifactBuilds {
		t.Run(artifact.name, func(t *testing.T) {
			assert.Contains(t, artifact.script, artifact.assignment)
			assert.Equal(t, 1, strings.Count(artifact.script, expectedTarget), "artifact must stamp common.Version exactly once")
		})
	}

	electronBuildScript := string(readRepositoryFile(t, filepath.Join(repositoryRoot, "electron", "build.sh")))
	assert.Equal(t, 4, strings.Count(electronBuildScript, "-X="+expectedTarget+"=${VERSION}"),
		"every local Electron OS branch must stamp the requested version")
}

func TestNativeReleaseArtifactsVerifyLinkedVersion(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	releaseWorkflow := loadArtifactWorkflow(t, filepath.Join(repositoryRoot, ".github", "workflows", "release.yml"))
	electronWorkflow := loadArtifactWorkflow(t, filepath.Join(repositoryRoot, ".github", "workflows", "electron-build.yml"))

	nativeBuilds := []struct {
		name   string
		script string
		binary string
	}{
		{
			name:   "Linux amd64 release",
			script: namedStep(t, artifactJobByName(t, releaseWorkflow, "linux"), "Build Backend (amd64)").Run,
			binary: "./new-api-${VERSION}",
		},
		{
			name:   "macOS release",
			script: namedStep(t, artifactJobByName(t, releaseWorkflow, "macos"), "Build Backend").Run,
			binary: "./new-api-macos-${VERSION}",
		},
		{
			name:   "Windows release",
			script: namedStep(t, artifactJobByName(t, releaseWorkflow, "windows"), "Build Backend").Run,
			binary: "./new-api-${VERSION}.exe",
		},
		{
			name:   "Electron Windows release",
			script: namedStep(t, artifactJobByName(t, electronWorkflow, "build"), "Build Go binary (Windows)").Run,
			binary: "./new-api.exe",
		},
	}

	for _, artifact := range nativeBuilds {
		t.Run(artifact.name, func(t *testing.T) {
			assert.Contains(t, artifact.script, `env -u VERSION "`+artifact.binary+`" --version`)
			assert.Contains(t, artifact.script, `test "$observed_version" = "$VERSION"`)
		})
	}

	electronBuildScript := string(readRepositoryFile(t, filepath.Join(repositoryRoot, "electron", "build.sh")))
	assert.Contains(t, electronBuildScript, "verify_linked_version()")
	assert.Contains(t, electronBuildScript, "env -u VERSION")
	assert.Contains(t, electronBuildScript, "test ! -e .env")
	assert.Equal(t, 4, strings.Count(electronBuildScript, "verify_linked_version \""),
		"every local Electron OS branch must verify its linked version")
}

func TestLocalArtifactBuildsResolveNonEmptyVersion(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	makefile := string(readRepositoryFile(t, filepath.Join(repositoryRoot, "makefile")))
	dockerfile := string(readRepositoryFile(t, filepath.Join(repositoryRoot, "Dockerfile")))

	assert.Contains(t, makefile, `version="$${version:-v0.0.0}"`)
	assert.Equal(t, 2, strings.Count(dockerfile, `VERSION="${VERSION:-v0.0.0}"`),
		"container frontend and backend must resolve the same nonempty fallback")
}

func modulePath(t *testing.T, repositoryRoot string) string {
	t.Helper()
	goMod := string(readRepositoryFile(t, filepath.Join(repositoryRoot, "go.mod")))
	for _, line := range strings.Split(goMod, "\n") {
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	require.FailNow(t, "go.mod must declare a module path")
	return ""
}

type artifactWorkflow struct {
	Jobs map[string]artifactJob `yaml:"jobs"`
}

type artifactJob struct {
	Steps []artifactStep `yaml:"steps"`
}

type artifactStep struct {
	Name string `yaml:"name"`
	Run  string `yaml:"run"`
}

func loadArtifactWorkflow(t *testing.T, path string) artifactWorkflow {
	t.Helper()
	var workflow artifactWorkflow
	require.NoError(t, yaml.Unmarshal(readRepositoryFile(t, path), &workflow))
	require.NotEmpty(t, workflow.Jobs)
	return workflow
}

func artifactJobByName(t *testing.T, workflow artifactWorkflow, name string) artifactJob {
	t.Helper()
	job, ok := workflow.Jobs[name]
	require.True(t, ok, "workflow must define the %q job", name)
	return job
}

func namedStep(t *testing.T, job artifactJob, name string) artifactStep {
	t.Helper()
	for _, step := range job.Steps {
		if step.Name == name {
			return step
		}
	}
	require.FailNow(t, "workflow job must contain named step", "%q", name)
	return artifactStep{}
}

func readRepositoryFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}
