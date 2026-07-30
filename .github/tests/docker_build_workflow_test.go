package workflow_test

import (
	"fmt"
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

const (
	buildSingleArchJob = "build_single_arch"
	createManifestsJob = "create_manifests"
)

var (
	fullCommitActionPattern = regexp.MustCompile(`^[^@[:space:]]+@[0-9a-fA-F]{40}$`)
	githubExpressionPattern = regexp.MustCompile(`\$\{\{[^}]+}}`)
)

type dockerBuildWorkflow struct {
	Concurrency dockerBuildConcurrency    `yaml:"concurrency"`
	Jobs        map[string]dockerBuildJob `yaml:"jobs"`
}

type dockerBuildConcurrency struct {
	Group            string `yaml:"group"`
	CancelInProgress bool   `yaml:"cancel-in-progress"`
}

type dockerBuildJob struct {
	Needs       []string          `yaml:"needs"`
	Permissions map[string]string `yaml:"permissions"`
	Steps       []dockerBuildStep `yaml:"steps"`
}

type dockerBuildStep struct {
	Name string         `yaml:"name"`
	Uses string         `yaml:"uses"`
	If   string         `yaml:"if"`
	Run  string         `yaml:"run"`
	With map[string]any `yaml:"with"`
}

type imageToolsCreate struct {
	StepIndex int
	Targets   []string
	Sources   []string
}

func TestArchitectureBuildStagesAndSignsImmutableDigest(t *testing.T) {
	workflow := loadDockerBuildWorkflow(t)
	job := requireJob(t, workflow, buildSingleArchJob)

	buildIndex := stepIndex(job, func(step dockerBuildStep) bool {
		return strings.HasPrefix(step.Uses, "docker/build-push-action@")
	})
	installIndex := stepIndex(job, func(step dockerBuildStep) bool {
		return strings.HasPrefix(step.Uses, "sigstore/cosign-installer@")
	})
	readinessIndex := stepIndex(job, func(step dockerBuildStep) bool {
		return commandLines(step.Run, "cosign", "version") != nil
	})
	signIndex := stepIndex(job, func(step dockerBuildStep) bool {
		return commandLines(step.Run, "cosign", "sign") != nil
	})

	require.NotEqual(t, -1, buildIndex, "build_single_arch must build and push an image")
	require.NotEqual(t, -1, installIndex, "build_single_arch must install cosign")
	require.NotEqual(t, -1, readinessIndex, "build_single_arch must check cosign readiness")
	require.NotEqual(t, -1, signIndex, "build_single_arch must sign its image")
	assert.Less(t, installIndex, buildIndex, "cosign readiness must be checked before the staged registry write")
	assert.Less(t, readinessIndex, buildIndex, "cosign must run successfully before the staged registry write")
	assert.Less(t, buildIndex, signIndex, "the staged digest must exist before it is signed")

	tags, ok := job.Steps[buildIndex].With["tags"].(string)
	require.True(t, ok, "docker/build-push-action must define string tags")
	var nonStagedTags []string
	for _, tag := range nonEmptyLines(tags) {
		if !isStagingRef(tag) {
			nonStagedTags = append(nonStagedTags, tag)
		}
	}
	assert.Empty(t, nonStagedTags, "architecture builds may push only run-scoped staging tags")

	signCommands := commandLines(job.Steps[signIndex].Run, "cosign", "sign")
	require.Len(t, signCommands, 1, "each architecture digest must be signed exactly once")
	assert.True(t, strings.HasSuffix(lastOperand(signCommands[0]), "@${{steps.build.outputs.digest}}"),
		"architecture signing must address the immutable build digest")
}

func TestManifestStagesSignedDigestsBeforeFinalPromotion(t *testing.T) {
	workflow := loadDockerBuildWorkflow(t)
	job := requireJob(t, workflow, createManifestsJob)
	assert.Contains(t, job.Needs, buildSingleArchJob, "manifest publication must wait for all signed architecture builds")

	installIndex := stepIndex(job, func(step dockerBuildStep) bool {
		return strings.HasPrefix(step.Uses, "sigstore/cosign-installer@")
	})
	verifyArchIndex := stepIndex(job, func(step dockerBuildStep) bool {
		commands := commandLines(step.Run, "cosign", "verify")
		return containsLastOperand(commands, "$IMAGE@$AMD64_DIGEST") && containsLastOperand(commands, "$IMAGE@$ARM64_DIGEST")
	})
	signManifestIndex := stepIndex(job, func(step dockerBuildStep) bool {
		return containsLastOperand(commandLines(step.Run, "cosign", "sign"), "$IMAGE@$MANIFEST_DIGEST")
	})

	require.NotEqual(t, -1, installIndex, "create_manifests must install cosign")
	require.NotEqual(t, -1, verifyArchIndex, "create_manifests must verify both staged architecture digests")
	require.NotEqual(t, -1, signManifestIndex, "create_manifests must sign the staged multi-arch digest")
	assert.Less(t, installIndex, verifyArchIndex, "cosign must be ready before staged signatures are verified")

	creates := imageToolsCreates(job)
	var stagedManifest *imageToolsCreate
	finalTargets := make(map[string]imageToolsCreate)
	for i := range creates {
		create := creates[i]
		for _, target := range create.Targets {
			if target == "$IMAGE:$STAGING_PREFIX-manifest" {
				stagedManifest = &create
			}
			if isFinalRef(target) {
				finalTargets[target] = create
			}
		}
	}

	require.NotNil(t, stagedManifest, "the multi-arch index must be assembled under a staging reference")
	assert.ElementsMatch(t, []string{"$IMAGE@$AMD64_DIGEST", "$IMAGE@$ARM64_DIGEST"}, stagedManifest.Sources,
		"the staged multi-arch index must contain the verified architecture digests")
	assert.Less(t, verifyArchIndex, stagedManifest.StepIndex, "architecture signatures must be verified before index assembly")
	assert.Less(t, stagedManifest.StepIndex, signManifestIndex, "the staged index digest must exist before signing")

	expectedPromotions := map[string]string{
		"$IMAGE:$TAG-amd64":   "$IMAGE@$AMD64_DIGEST",
		"$IMAGE:$TAG-arm64":   "$IMAGE@$ARM64_DIGEST",
		"$IMAGE:$TAG":         "$IMAGE@$MANIFEST_DIGEST",
		"$IMAGE:latest-amd64": "$IMAGE@$AMD64_DIGEST",
		"$IMAGE:latest-arm64": "$IMAGE@$ARM64_DIGEST",
		"$IMAGE:latest":       "$IMAGE@$MANIFEST_DIGEST",
	}
	for target, signedDigest := range expectedPromotions {
		promotion, ok := finalTargets[target]
		require.True(t, ok, "missing final promotion target %s", target)
		assert.Equal(t, []string{signedDigest}, promotion.Sources,
			"final target %s must be promoted from its signed immutable digest", target)
		assert.Less(t, signManifestIndex, promotion.StepIndex,
			"final target %s must not be published before multi-arch signing succeeds", target)
	}
	assert.Len(t, finalTargets, len(expectedPromotions), "only the expected architecture and manifest tags may be promoted")
}

func TestReleaseConcurrencySerializesSharedTagsWithoutCancellation(t *testing.T) {
	workflow := loadDockerBuildWorkflow(t)
	require.NotEmpty(t, workflow.Concurrency.Group, "release publication needs a shared concurrency group")
	assert.False(t, workflow.Concurrency.CancelInProgress, "an in-progress release must not be cancelled during publication")

	group := workflow.Concurrency.Group
	for _, runSpecificValue := range []string{"github.ref", "github.sha", "github.run_id", "github.run_attempt", "inputs.tag"} {
		assert.NotContains(t, group, runSpecificValue,
			"the concurrency group must be shared by releases that can mutate latest")
	}
}

func TestReleaseActionsAreImmutableAndPermissionsAreBounded(t *testing.T) {
	workflow := loadDockerBuildWorkflow(t)
	var mutableActions []string
	for jobName, job := range workflow.Jobs {
		for _, step := range job.Steps {
			if step.Uses == "" || strings.HasPrefix(step.Uses, "./") || strings.HasPrefix(step.Uses, "docker://") {
				continue
			}
			if !fullCommitActionPattern.MatchString(step.Uses) {
				mutableActions = append(mutableActions, jobName+" / "+step.Name+": "+step.Uses)
			}
		}
	}
	sort.Strings(mutableActions)
	assert.Empty(t, mutableActions, "release jobs use mutable external action references")

	assert.Equal(t, map[string]string{
		"contents": "read",
		"packages": "write",
		"id-token": "write",
	}, requireJob(t, workflow, buildSingleArchJob).Permissions)
	assert.Equal(t, map[string]string{
		"packages": "write",
		"id-token": "write",
	}, requireJob(t, workflow, createManifestsJob).Permissions)
}

func TestAllPrerequisitesPrecedeGHCRFinalPublication(t *testing.T) {
	workflow := loadDockerBuildWorkflow(t)
	job := requireJob(t, workflow, createManifestsJob)
	firstFinalPublication := len(job.Steps)
	for _, create := range imageToolsCreates(job) {
		for _, target := range create.Targets {
			if isFinalRef(target) && create.StepIndex < firstFinalPublication {
				firstFinalPublication = create.StepIndex
			}
		}
	}
	require.Less(t, firstFinalPublication, len(job.Steps), "create_manifests must promote final GHCR tags")

	for index, step := range job.Steps {
		stepText := strings.ToLower(step.Name + "\n" + step.Uses + "\n" + step.If + "\n" + step.Run + "\n" + fmt.Sprint(step.With))
		isPrerequisite := strings.HasPrefix(step.Uses, "docker/login-action@") ||
			strings.HasPrefix(step.Uses, "docker/setup-buildx-action@") ||
			strings.HasPrefix(step.Uses, "sigstore/cosign-installer@") ||
			strings.Contains(stepText, "cosign version") ||
			strings.Contains(stepText, "cosign verify")
		if isPrerequisite {
			assert.Less(t, index, firstFinalPublication,
				"registry and signing prerequisites must finish before GHCR final publication")
		}

	}

	for jobName, candidateJob := range workflow.Jobs {
		for index, step := range candidateJob.Steps {
			stepText := strings.ToLower(step.Name + "\n" + step.Uses + "\n" + step.If + "\n" + step.Run + "\n" + fmt.Sprint(step.With))
			isDockerHubStep := strings.Contains(stepText, "dockerhub_") || strings.Contains(stepText, "docker.io")
			if !isDockerHubStep {
				continue
			}
			assert.NotEmpty(t, step.If, "%s step %d must be guarded when Docker Hub credentials are absent", jobName, index)
			if jobName == createManifestsJob {
				assert.Less(t, index, firstFinalPublication,
					"optional Docker Hub work cannot fail after GHCR final publication")
			}
		}
	}
}

func loadDockerBuildWorkflow(t *testing.T) dockerBuildWorkflow {
	t.Helper()

	workflowPath := filepath.Join("..", "workflows", "docker-build.yml")
	data, err := os.ReadFile(workflowPath)
	require.NoError(t, err)

	var workflow dockerBuildWorkflow
	require.NoError(t, yaml.Unmarshal(data, &workflow))
	require.NotEmpty(t, workflow.Jobs)
	return workflow
}

func requireJob(t *testing.T, workflow dockerBuildWorkflow, name string) dockerBuildJob {
	t.Helper()
	job, ok := workflow.Jobs[name]
	require.True(t, ok, "workflow must define the %q job", name)
	return job
}

func stepIndex(job dockerBuildJob, matches func(dockerBuildStep) bool) int {
	for index, step := range job.Steps {
		if matches(step) {
			return index
		}
	}
	return -1
}

func imageToolsCreates(job dockerBuildJob) []imageToolsCreate {
	var creates []imageToolsCreate
	for stepIndex, step := range job.Steps {
		for _, command := range commandLines(step.Run, "docker", "buildx", "imagetools", "create") {
			create := imageToolsCreate{StepIndex: stepIndex}
			for index := 4; index < len(command); index++ {
				switch command[index] {
				case "-t", "--tag":
					if index+1 < len(command) {
						create.Targets = append(create.Targets, command[index+1])
						index++
					}
				default:
					if !strings.HasPrefix(command[index], "-") {
						create.Sources = append(create.Sources, command[index])
					}
				}
			}
			creates = append(creates, create)
		}
	}
	return creates
}

func commandLines(script string, prefix ...string) [][]string {
	normalized := strings.ReplaceAll(script, "\\\n", " ")
	normalized = githubExpressionPattern.ReplaceAllStringFunc(normalized, func(expression string) string {
		return strings.ReplaceAll(expression, " ", "")
	})
	var commands [][]string
	for _, line := range strings.Split(normalized, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		for index := range fields {
			fields[index] = strings.Trim(fields[index], `"'`)
		}
		if len(fields) >= len(prefix) && equalStrings(fields[:len(prefix)], prefix) {
			commands = append(commands, fields)
		}
	}
	return commands
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func containsLastOperand(commands [][]string, operand string) bool {
	for _, command := range commands {
		if lastOperand(command) == operand {
			return true
		}
	}
	return false
}

func lastOperand(command []string) string {
	if len(command) == 0 {
		return ""
	}
	return strings.Trim(command[len(command)-1], `"'`)
}

func nonEmptyLines(value string) []string {
	var lines []string
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func isStagingRef(ref string) bool {
	return strings.Contains(ref, "STAGING_PREFIX")
}

func isFinalRef(ref string) bool {
	if isStagingRef(ref) {
		return false
	}
	return strings.Contains(ref, ":$TAG") ||
		strings.Contains(ref, ":${TAG}") ||
		strings.Contains(ref, ":latest")
}
