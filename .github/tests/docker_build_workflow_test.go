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
	buildSingleArchJob       = "build_single_arch"
	createManifestsJob       = "create_manifests"
	promoteLatestJob         = "promote_latest"
	releaseCandidateArtifact = "docker-build-release-candidate"
)

var (
	fullCommitActionPattern = regexp.MustCompile(`^[^@[:space:]]+@[0-9a-fA-F]{40}$`)
	githubExpressionPattern = regexp.MustCompile(`\$\{\{[^}]+}}`)
)

type dockerBuildWorkflow struct {
	Concurrency *dockerBuildConcurrency   `yaml:"concurrency"`
	Jobs        map[string]dockerBuildJob `yaml:"jobs"`
	RunName     string                    `yaml:"run-name"`
}

type dockerBuildConcurrency struct {
	Group            string `yaml:"group"`
	CancelInProgress *bool  `yaml:"cancel-in-progress"`
}

type dockerBuildJob struct {
	Name           string                  `yaml:"name"`
	Needs          []string                `yaml:"needs"`
	Concurrency    *dockerBuildConcurrency `yaml:"concurrency"`
	Env            map[string]string       `yaml:"env"`
	If             string                  `yaml:"if"`
	Outputs        map[string]string       `yaml:"outputs"`
	Permissions    map[string]string       `yaml:"permissions"`
	Steps          []dockerBuildStep       `yaml:"steps"`
	TimeoutMinutes int                     `yaml:"timeout-minutes"`
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

func TestVersionTagNamespaceCannotOverlapSharedOrSiblingTags(t *testing.T) {
	workflow := loadDockerBuildWorkflow(t)
	job := requireJob(t, workflow, buildSingleArchJob)
	resolveIndex := stepIndex(job, func(step dockerBuildStep) bool {
		return strings.Contains(step.Name, "Resolve tag")
	})
	require.NotEqual(t, -1, resolveIndex, "architecture build must validate its version tag")
	validation := job.Steps[resolveIndex].Run
	assert.Contains(t, validation, `^[a-z0-9_][a-z0-9_.-]{0,121}$`,
		"version tags must leave room for architecture suffixes")
	for _, reserved := range []string{"latest", "staging-*", "*-amd64", "*-arm64"} {
		assert.Contains(t, validation, reserved,
			"version tag validation must reject reserved namespace %s", reserved)
	}
}

func TestWorkflowRunNameBindsManualDispatchToItsReleaseTag(t *testing.T) {
	workflow := loadDockerBuildWorkflow(t)
	assert.Equal(t, "Publish Docker image ${{ github.event.inputs.tag || github.ref_name }}", workflow.RunName,
		"the run API must expose the same tag used by push and workflow_dispatch releases")
}

func TestReleaseCandidateRecordsAuthoritativeWorkflowSHA(t *testing.T) {
	job := requireJob(t, loadDockerBuildWorkflow(t), createManifestsJob)
	recordIndex := stepIndex(job, func(step dockerBuildStep) bool {
		return step.Name == "Record immutable release candidate"
	})
	require.NotEqual(t, -1, recordIndex)
	recordScript := job.Steps[recordIndex].Run
	assert.Contains(t, recordScript, `--arg head_sha "$GITHUB_SHA"`)
	assert.Contains(t, recordScript, `head_sha: $head_sha`)
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
		"$IMAGE:$TAG-amd64": "$IMAGE@$AMD64_DIGEST",
		"$IMAGE:$TAG-arm64": "$IMAGE@$ARM64_DIGEST",
		"$IMAGE:$TAG":       "$IMAGE@$MANIFEST_DIGEST",
	}
	for target, signedDigest := range expectedPromotions {
		promotion, ok := finalTargets[target]
		require.True(t, ok, "missing final promotion target %s", target)
		assert.Equal(t, []string{signedDigest}, promotion.Sources,
			"final target %s must be promoted from its signed immutable digest", target)
		assert.Less(t, signManifestIndex, promotion.StepIndex,
			"final target %s must not be published before multi-arch signing succeeds", target)
	}
	assert.Len(t, finalTargets, len(expectedPromotions), "immutable publication may promote only version tags")

	versionPromotion := finalTargets["$IMAGE:$TAG"]
	promotionScript := job.Steps[versionPromotion.StepIndex].Run
	firstInspect := strings.Index(promotionScript, "docker buildx imagetools inspect")
	firstCreate := strings.Index(promotionScript, "docker buildx imagetools create")
	require.NotEqual(t, -1, firstInspect, "version promotion must inspect an existing tag before mutation")
	require.NotEqual(t, -1, firstCreate, "version promotion must create missing immutable tags")
	assert.Less(t, firstInspect, firstCreate, "existing immutable tags must be checked before any overwrite")
	assert.Contains(t, promotionScript, "manifest unknown", "only an authoritative missing-tag response may permit creation")
	assert.Contains(t, promotionScript, "Refusing to overwrite immutable version tag")
}

func TestImmutablePublicationIsOutsideReplaceableSharedConcurrency(t *testing.T) {
	workflow := loadDockerBuildWorkflow(t)
	assert.Nil(t, workflow.Concurrency,
		"workflow-level concurrency can replace pending releases before immutable publication")
	assert.Nil(t, requireJob(t, workflow, buildSingleArchJob).Concurrency,
		"architecture staging must not enter a shared native concurrency queue")
	versionConcurrency := requireJob(t, workflow, createManifestsJob).Concurrency
	require.NotNil(t, versionConcurrency, "duplicate publication of one version tag must be serialized")
	require.NotNil(t, versionConcurrency.CancelInProgress)
	assert.False(t, *versionConcurrency.CancelInProgress,
		"a running immutable version publication must not be cancelled")
	assert.Contains(t, versionConcurrency.Group, "needs.build_single_arch.outputs.tag",
		"distinct version tags must never share a replaceable pending queue")

	for jobName, job := range workflow.Jobs {
		if jobName == promoteLatestJob {
			continue
		}
		for _, create := range imageToolsCreates(job) {
			for _, target := range create.Targets {
				assert.False(t, isLatestRef(target),
					"%s must finish without mutating shared latest tag %s", jobName, target)
			}
		}
	}
}

func TestLatestPromotionIsSerializedAfterImmutablePublication(t *testing.T) {
	workflow := loadDockerBuildWorkflow(t)
	versionJob := requireJob(t, workflow, createManifestsJob)
	latestJob := requireJob(t, workflow, promoteLatestJob)
	assert.Equal(t, "Create multi-arch manifests", versionJob.Name,
		"the reconciliation helper binds immutable success to this GitHub job name")

	assert.Equal(t, []string{createManifestsJob}, latestJob.Needs,
		"latest reconciliation must run only after successful immutable publication")
	assert.Empty(t, latestJob.If, "latest reconciliation must retain the default successful-needs gate")
	require.NotNil(t, latestJob.Concurrency, "only latest reconciliation needs a shared concurrency group")
	require.NotNil(t, latestJob.Concurrency.CancelInProgress,
		"latest reconciliation must explicitly preserve the running critical section")
	assert.False(t, *latestJob.Concurrency.CancelInProgress,
		"a running latest reconciliation must not be cancelled mid-promotion")
	require.NotEmpty(t, latestJob.Concurrency.Group)
	for _, runSpecificValue := range []string{"github.ref", "github.sha", "github.run_id", "github.run_attempt", "inputs.tag"} {
		assert.NotContains(t, latestJob.Concurrency.Group, runSpecificValue,
			"the latest concurrency group must be shared by every release")
	}
	assert.Positive(t, latestJob.TimeoutMinutes, "latest reconciliation must have a bounded timeout")
	assert.LessOrEqual(t, latestJob.TimeoutMinutes, 15, "latest reconciliation must fail instead of waiting indefinitely")

	lastVersionPromotion := lastTargetIndex(versionJob, isVersionRef)
	require.NotEqual(t, -1, lastVersionPromotion, "create_manifests must publish immutable version tags")
	markerIndex := stepIndex(versionJob, func(step dockerBuildStep) bool {
		return strings.HasPrefix(step.Uses, "actions/upload-artifact@")
	})
	require.NotEqual(t, -1, markerIndex, "immutable publication must record a reconciliation candidate")
	assert.Less(t, lastVersionPromotion, markerIndex,
		"a release may become eligible for latest only after every immutable version tag is published")
	markerStep := versionJob.Steps[markerIndex]
	assert.Equal(t, "Publish immutable release candidate marker", markerStep.Name,
		"the reconciliation helper binds cancellation recovery to this GitHub step name")
	assert.Empty(t, markerStep.If, "candidate marker upload must stop when immutable publication fails")
	assert.Equal(t, releaseCandidateArtifact, markerStep.With["name"],
		"the publisher and reconciler must share one candidate artifact name")
	assert.Equal(t, "error", markerStep.With["if-no-files-found"],
		"a missing completion marker must fail closed")
	markerPath, ok := markerStep.With["path"].(string)
	require.True(t, ok, "candidate marker path must be a string")
	assert.True(t, strings.HasSuffix(markerPath, "/candidate.json"),
		"candidate marker must contain the helper's expected candidate.json")

	electionIndex := stepIndex(latestJob, func(step dockerBuildStep) bool {
		return strings.Contains(step.Run, ".github/releasequeue/reconcile_latest.py")
	})
	require.NotEqual(t, -1, electionIndex, "latest reconciliation must re-elect the newest completed release")
	firstLatestPromotion := firstTargetIndex(latestJob, isLatestRef)
	require.NotEqual(t, -1, firstLatestPromotion, "promote_latest must publish shared latest tags")
	assert.Less(t, electionIndex, firstLatestPromotion,
		"newest-candidate election must succeed before any shared latest mutation")

	expectedPromotions := map[string]string{
		"$IMAGE:latest-amd64": "$IMAGE@$AMD64_DIGEST",
		"$IMAGE:latest-arm64": "$IMAGE@$ARM64_DIGEST",
		"$IMAGE:latest":       "$IMAGE@$MANIFEST_DIGEST",
	}
	actualPromotions := make(map[string]imageToolsCreate)
	for _, create := range imageToolsCreates(latestJob) {
		for _, target := range create.Targets {
			if isLatestRef(target) {
				actualPromotions[target] = create
				assert.Equal(t, "steps.candidate.outputs.latest_eligible == 'true'", latestJob.Steps[create.StepIndex].If,
					"latest mutation must require a successful eligible election")
			}
			assert.False(t, isVersionRef(target), "latest reconciliation must not rewrite immutable version tag %s", target)
		}
	}
	for target, digest := range expectedPromotions {
		promotion, ok := actualPromotions[target]
		require.True(t, ok, "missing latest promotion target %s", target)
		assert.Equal(t, []string{digest}, promotion.Sources,
			"latest target %s must use the elected release's signed digest", target)
	}
	assert.Len(t, actualPromotions, len(expectedPromotions), "only the expected latest aliases may be reconciled")
}

func TestLatestEligibilityIsKnownBeforeRegistryWork(t *testing.T) {
	workflow := loadDockerBuildWorkflow(t)
	versionJob := requireJob(t, workflow, createManifestsJob)
	latestJob := requireJob(t, workflow, promoteLatestJob)
	assert.Equal(t, "${{ needs.build_single_arch.outputs.tag }}", versionJob.Outputs["tag"])
	assert.Equal(t, "${{ needs.create_manifests.outputs.tag }}", latestJob.Env["CURRENT_TAG"])

	electionIndex := stepIndex(latestJob, func(step dockerBuildStep) bool {
		return strings.Contains(step.Run, ".github/releasequeue/reconcile_latest.py")
	})
	require.NotEqual(t, -1, electionIndex)
	registrySetupIndex := stepIndex(latestJob, func(step dockerBuildStep) bool {
		return strings.HasPrefix(step.Uses, "docker/setup-buildx-action@")
	})
	require.NotEqual(t, -1, registrySetupIndex)
	assert.Less(t, electionIndex, registrySetupIndex,
		"latest eligibility must be known before registry and signing setup")
	guard := "steps.candidate.outputs.latest_eligible == 'true'"
	for index := electionIndex + 1; index < len(latestJob.Steps); index++ {
		assert.Equal(t, guard, latestJob.Steps[index].If,
			"latest step %q must be skipped for a non-SemVer tag", latestJob.Steps[index].Name)
	}
}

func TestConcurrencyUsesActionlintSupportedSyntax(t *testing.T) {
	data := readDockerBuildWorkflow(t)
	var document yaml.Node
	require.NoError(t, yaml.Unmarshal(data, &document))

	var unsupportedPaths []string
	findUnsupportedConcurrencyKeys(&document, nil, &unsupportedPaths)
	assert.Empty(t, unsupportedPaths, "concurrency contains unsupported keys; queue is not valid GitHub Actions syntax")
}

func TestReleaseJobsHaveBoundedTimeouts(t *testing.T) {
	workflow := loadDockerBuildWorkflow(t)
	maximumTimeouts := map[string]int{
		buildSingleArchJob: 180,
		createManifestsJob: 60,
		promoteLatestJob:   15,
	}
	for jobName, maximum := range maximumTimeouts {
		job := requireJob(t, workflow, jobName)
		assert.Positive(t, job.TimeoutMinutes, "%s must not consume a runner indefinitely", jobName)
		assert.LessOrEqual(t, job.TimeoutMinutes, maximum, "%s timeout exceeds its bounded release window", jobName)
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
	assert.Equal(t, map[string]string{
		"actions":  "read",
		"contents": "read",
		"packages": "write",
	}, requireJob(t, workflow, promoteLatestJob).Permissions)
}

func TestAllPrerequisitesPrecedeConsumerTagMutation(t *testing.T) {
	workflow := loadDockerBuildWorkflow(t)
	versionJob := requireJob(t, workflow, createManifestsJob)
	latestJob := requireJob(t, workflow, promoteLatestJob)
	require.Contains(t, latestJob.Needs, createManifestsJob,
		"signing and immutable publication failures must prevent latest reconciliation")

	assertPrerequisitesBeforeTargetMutation(t, versionJob, isVersionRef,
		"registry and signing prerequisites must finish before immutable version publication")
	assertPrerequisitesBeforeTargetMutation(t, latestJob, isLatestRef,
		"candidate election, signature verification, and registry prerequisites must finish before latest promotion")

	for jobName, candidateJob := range workflow.Jobs {
		for index, step := range candidateJob.Steps {
			stepText := strings.ToLower(step.Name + "\n" + step.Uses + "\n" + step.If + "\n" + step.Run + "\n" + fmt.Sprint(step.With))
			isDockerHubStep := strings.Contains(stepText, "dockerhub_") || strings.Contains(stepText, "docker.io")
			if !isDockerHubStep {
				continue
			}
			assert.NotEmpty(t, step.If, "%s step %d must be guarded when Docker Hub credentials are absent", jobName, index)
			firstMutation := firstConsumerTagIndex(candidateJob)
			if firstMutation != -1 {
				assert.Less(t, index, firstMutation,
					"optional Docker Hub work cannot fail after GHCR consumer-tag publication")
			}
		}
	}
}

func loadDockerBuildWorkflow(t *testing.T) dockerBuildWorkflow {
	t.Helper()

	data := readDockerBuildWorkflow(t)
	var workflow dockerBuildWorkflow
	require.NoError(t, yaml.Unmarshal(data, &workflow))
	require.NotEmpty(t, workflow.Jobs)
	return workflow
}

func readDockerBuildWorkflow(t *testing.T) []byte {
	t.Helper()
	workflowPath := filepath.Join("..", "workflows", "docker-build.yml")
	data, err := os.ReadFile(workflowPath)
	require.NoError(t, err)
	return data
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

func firstTargetIndex(job dockerBuildJob, matches func(string) bool) int {
	first := -1
	for _, create := range imageToolsCreates(job) {
		for _, target := range create.Targets {
			if matches(target) && (first == -1 || create.StepIndex < first) {
				first = create.StepIndex
			}
		}
	}
	return first
}

func lastTargetIndex(job dockerBuildJob, matches func(string) bool) int {
	last := -1
	for _, create := range imageToolsCreates(job) {
		for _, target := range create.Targets {
			if matches(target) && create.StepIndex > last {
				last = create.StepIndex
			}
		}
	}
	return last
}

func firstConsumerTagIndex(job dockerBuildJob) int {
	return firstTargetIndex(job, func(target string) bool {
		return isVersionRef(target) || isLatestRef(target)
	})
}

func assertPrerequisitesBeforeTargetMutation(
	t *testing.T,
	job dockerBuildJob,
	targetMatches func(string) bool,
	message string,
) {
	t.Helper()
	firstMutation := firstTargetIndex(job, targetMatches)
	require.NotEqual(t, -1, firstMutation, "job must mutate its expected consumer tags")

	for index, step := range job.Steps {
		stepText := strings.ToLower(step.Name + "\n" + step.Uses + "\n" + step.If + "\n" + step.Run + "\n" + fmt.Sprint(step.With))
		isPrerequisite := strings.HasPrefix(step.Uses, "docker/login-action@") ||
			strings.HasPrefix(step.Uses, "docker/setup-buildx-action@") ||
			strings.HasPrefix(step.Uses, "sigstore/cosign-installer@") ||
			strings.Contains(stepText, "cosign version") ||
			strings.Contains(stepText, "cosign verify") ||
			strings.Contains(stepText, "reconcile_latest.py") ||
			strings.Contains(strings.ToLower(step.Name), "validate digest-preserving")
		if isPrerequisite {
			assert.Less(t, index, firstMutation, message)
		}
	}
}

func findUnsupportedConcurrencyKeys(node *yaml.Node, path []string, unsupportedPaths *[]string) {
	if node.Kind == yaml.MappingNode {
		for index := 0; index+1 < len(node.Content); index += 2 {
			key := node.Content[index]
			value := node.Content[index+1]
			nextPath := append(append([]string(nil), path...), key.Value)
			if key.Value == "concurrency" && value.Kind == yaml.MappingNode {
				for childIndex := 0; childIndex+1 < len(value.Content); childIndex += 2 {
					childKey := value.Content[childIndex].Value
					if childKey != "group" && childKey != "cancel-in-progress" {
						*unsupportedPaths = append(*unsupportedPaths, strings.Join(append(nextPath, childKey), "."))
					}
				}
			}
			findUnsupportedConcurrencyKeys(value, nextPath, unsupportedPaths)
		}
		return
	}

	for _, child := range node.Content {
		findUnsupportedConcurrencyKeys(child, path, unsupportedPaths)
	}
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

func isVersionRef(ref string) bool {
	if isStagingRef(ref) {
		return false
	}
	return strings.Contains(ref, ":$TAG") || strings.Contains(ref, ":${TAG}")
}

func isLatestRef(ref string) bool {
	return !isStagingRef(ref) && strings.Contains(ref, ":latest")
}

func isFinalRef(ref string) bool {
	return isVersionRef(ref) || isLatestRef(ref)
}
