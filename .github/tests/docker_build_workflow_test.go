package workflow_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const createManifestsJob = "create_manifests"

var (
	fullCommitActionPattern = regexp.MustCompile(`^[^@[:space:]]+@[0-9a-fA-F]{40}$`)
	publishedTargetPattern  = regexp.MustCompile(`(?:^|\s)-t\s+([^\s\\]+)`)
)

type dockerBuildWorkflow struct {
	Jobs map[string]dockerBuildJob `yaml:"jobs"`
}

type dockerBuildJob struct {
	Steps []dockerBuildStep `yaml:"steps"`
}

type dockerBuildStep struct {
	Name string `yaml:"name"`
	Uses string `yaml:"uses"`
	Run  string `yaml:"run"`
}

func TestCreateManifestsSignsOnlyPublishedRefs(t *testing.T) {
	job := loadCreateManifestsJob(t)
	publishedRefs := make(map[string]struct{})
	var signedRefs []string

	for _, step := range job.Steps {
		if strings.Contains(step.Run, "docker buildx imagetools create") {
			for _, match := range publishedTargetPattern.FindAllStringSubmatch(step.Run, -1) {
				publishedRefs[match[1]] = struct{}{}
			}
		}

		for _, line := range strings.Split(step.Run, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 3 || fields[0] != "cosign" || fields[1] != "sign" {
				continue
			}
			signedRefs = append(signedRefs, strings.Trim(fields[len(fields)-1], `"'`))
		}
	}

	require.NotEmpty(t, publishedRefs, "create_manifests must publish at least one manifest")
	require.NotEmpty(t, signedRefs, "create_manifests must sign at least one manifest")

	var unpublishedRefs []string
	for _, ref := range signedRefs {
		if _, ok := publishedRefs[ref]; !ok {
			unpublishedRefs = append(unpublishedRefs, ref)
		}
	}
	sort.Strings(unpublishedRefs)
	assert.Empty(t, unpublishedRefs, "create_manifests signs registry refs it does not publish")
}

func TestCreateManifestsActionsUseFullCommitSHAs(t *testing.T) {
	job := loadCreateManifestsJob(t)
	var mutableActions []string

	for _, step := range job.Steps {
		if step.Uses == "" || strings.HasPrefix(step.Uses, "./") || strings.HasPrefix(step.Uses, "docker://") {
			continue
		}
		if !fullCommitActionPattern.MatchString(step.Uses) {
			mutableActions = append(mutableActions, step.Name+": "+step.Uses)
		}
	}

	assert.Empty(t, mutableActions, "create_manifests uses mutable external action references")
}

func TestCreateManifestsInstallsCosignOnce(t *testing.T) {
	job := loadCreateManifestsJob(t)
	installerCount := 0

	for _, step := range job.Steps {
		if strings.HasPrefix(step.Uses, "sigstore/cosign-installer@") {
			installerCount++
		}
	}

	assert.Equal(t, 1, installerCount, "create_manifests must invoke the cosign installer exactly once")
}

func loadCreateManifestsJob(t *testing.T) dockerBuildJob {
	t.Helper()

	workflowPath := filepath.Join("..", "workflows", "docker-build.yml")
	data, err := os.ReadFile(workflowPath)
	require.NoError(t, err)

	var workflow dockerBuildWorkflow
	require.NoError(t, yaml.Unmarshal(data, &workflow))

	job, ok := workflow.Jobs[createManifestsJob]
	require.True(t, ok, "workflow must define the %q job", createManifestsJob)
	return job
}
