package workflow_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestProtectedReleaseMetadataKeepsProjectIdentity(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))

	var manifest struct {
		Name string `yaml:"name"`
	}
	require.NoError(t, yaml.Unmarshal(readRepositoryFile(t, filepath.Join(repositoryRoot, "web", "package.json")), &manifest))
	assert.Equal(t, "new-api-web-workspace", manifest.Name)

	var workflow struct {
		On          map[string]any    `yaml:"on"`
		Permissions map[string]string `yaml:"permissions"`
		Env         map[string]string `yaml:"env"`
	}
	require.NoError(t, yaml.Unmarshal(readRepositoryFile(t, filepath.Join(repositoryRoot, ".github", "workflows", "sync-to-gitee.yml")), &workflow))
	assert.Equal(t, map[string]any{"workflow_dispatch": workflow.On["workflow_dispatch"]}, workflow.On,
		"the mirror workflow must remain manual-only")
	assert.Equal(t, map[string]string{"contents": "read"}, workflow.Permissions)
	assert.Equal(t, "QuantumNous", workflow.Env["GITEE_OWNER"])
	assert.Equal(t, "new-api", workflow.Env["GITEE_REPO"])
}

func TestI18nWorkflowChecksConsolidatedFrontend(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	content := readRepositoryFile(t, filepath.Join(repositoryRoot, ".github", "workflows", "i18n-check.yml"))

	var workflow struct {
		On struct {
			PullRequest struct {
				Paths []string `yaml:"paths"`
			} `yaml:"pull_request"`
		} `yaml:"on"`
		Permissions map[string]string `yaml:"permissions"`
		Jobs        map[string]struct {
			Steps []struct {
				Name             string `yaml:"name"`
				WorkingDirectory string `yaml:"working-directory"`
				Run              string `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	require.NoError(t, yaml.Unmarshal(content, &workflow))
	assert.Equal(t, map[string]string{"contents": "read"}, workflow.Permissions)
	assert.ElementsMatch(t, []string{
		"web/src/**",
		"web/src/i18n/locales/**",
		"web/scripts/check-i18n-keys.mjs",
	}, workflow.On.PullRequest.Paths)

	job, ok := workflow.Jobs["i18n-check"]
	require.True(t, ok)
	var checkStep *struct {
		Name             string `yaml:"name"`
		WorkingDirectory string `yaml:"working-directory"`
		Run              string `yaml:"run"`
	}
	for index := range job.Steps {
		if job.Steps[index].Name == "Check i18n keys" {
			checkStep = &job.Steps[index]
			break
		}
	}
	require.NotNil(t, checkStep)
	assert.Equal(t, "web", checkStep.WorkingDirectory)
	assert.Equal(t, "node scripts/check-i18n-keys.mjs", checkStep.Run)
	assert.NotContains(t, string(content), "web/default")
}

func TestDevelopmentDockerDependencyLayerIncludesLocalModuleMetadata(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	dockerfile := string(readRepositoryFile(t, filepath.Join(repositoryRoot, "Dockerfile.dev")))

	rootModules := strings.Index(dockerfile, "ADD go.mod go.sum ./")
	localModule := strings.Index(dockerfile, "ADD relaykit/go.mod ./relaykit/go.mod")
	download := strings.Index(dockerfile, "RUN go mod download")
	require.NotEqual(t, -1, rootModules)
	require.NotEqual(t, -1, localModule)
	require.NotEqual(t, -1, download)
	assert.Less(t, rootModules, localModule)
	assert.Less(t, localModule, download,
		"the complete local module graph must exist before dependencies are resolved")
}

func TestRC62ChangelogDescribesVerifiedReleaseCoverage(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	changelog := string(readRepositoryFile(t, filepath.Join(repositoryRoot, "CHANGELOG.md")))
	rcHeading := "## [v1.0.0-rc.62] - 2026-08-03"

	assert.Equal(t, 1, strings.Count(changelog, rcHeading))
	unreleasedStart := strings.Index(changelog, "## [Unreleased]")
	rcStart := strings.Index(changelog, rcHeading)
	require.NotEqual(t, -1, unreleasedStart)
	require.Greater(t, rcStart, unreleasedStart)
	assert.Empty(t, strings.TrimSpace(changelog[unreleasedStart+len("## [Unreleased]"):rcStart]))

	rcEndOffset := strings.Index(changelog[rcStart+len(rcHeading):], "\n## [")
	require.NotEqual(t, -1, rcEndOffset)
	rcSection := changelog[rcStart : rcStart+len(rcHeading)+rcEndOffset]
	assert.Contains(t, rcSection,
		"Release builds now stamp exact version metadata across backend, frontend, Electron, and container artifacts, with build-time checks for frontend bundles and runnable native binaries.")
	assert.NotContains(t, rcSection,
		"stamp and verify exact version metadata across backend, frontend, Electron, and container artifacts")
}
