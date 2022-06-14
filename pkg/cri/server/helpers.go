/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package server

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/errdefs"
	clabels "github.com/containerd/containerd/labels"
	criconfig "github.com/containerd/containerd/pkg/cri/config"
	containerstore "github.com/containerd/containerd/pkg/cri/store/container"
	sandboxstore "github.com/containerd/containerd/pkg/cri/store/sandbox"
	runtimeoptions "github.com/containerd/containerd/pkg/runtimeoptions/v1"
	"github.com/containerd/containerd/plugin"
	"github.com/containerd/containerd/reference/docker"
	"github.com/containerd/containerd/runtime/linux/runctypes"
	runcoptions "github.com/containerd/containerd/runtime/v2/runc/options"
	"github.com/containerd/typeurl"
	imageidentity "github.com/opencontainers/image-spec/identity"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/sirupsen/logrus"

	runhcsoptions "github.com/Microsoft/hcsshim/cmd/containerd-shim-runhcs-v1/options"
	imagedigest "github.com/opencontainers/go-digest"
	"github.com/pelletier/go-toml"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

const (
	// errorStartReason is the exit reason when fails to start container.
	errorStartReason = "StartError"
	// errorStartExitCode is the exit code when fails to start container.
	// 128 is the same with Docker's behavior.
	// TODO(windows): Figure out what should be used for windows.
	errorStartExitCode = 128
	// completeExitReason is the exit reason when container exits with code 0.
	completeExitReason = "Completed"
	// errorExitReason is the exit reason when container exits with code non-zero.
	errorExitReason = "Error"
	// oomExitReason is the exit reason when process in container is oom killed.
	oomExitReason = "OOMKilled"

	// sandboxesDir contains all sandbox root. A sandbox root is the running
	// directory of the sandbox, all files created for the sandbox will be
	// placed under this directory.
	sandboxesDir = "sandboxes"
	// containersDir contains all container root.
	containersDir = "containers"
	// Delimiter used to construct container/sandbox names.
	nameDelimiter = "_"

	// criContainerdPrefix is common prefix for cri-containerd
	criContainerdPrefix = "io.cri-containerd"
	// containerKindLabel is a label key indicating container is sandbox container or application container
	containerKindLabel = criContainerdPrefix + ".kind"
	// containerKindSandbox is a label value indicating container is sandbox container
	containerKindSandbox = "sandbox"
	// containerKindContainer is a label value indicating container is application container
	containerKindContainer = "container"
	// imageLabelKey is the label key indicating the image is managed by cri plugin.
	imageLabelKey = criContainerdPrefix + ".image"
	// imageLabelValue is the label value indicating the image is managed by cri plugin.
	imageLabelValue = "managed"
	// sandboxMetadataExtension is an extension name that identify metadata of sandbox in CreateContainerRequest
	sandboxMetadataExtension = criContainerdPrefix + ".sandbox.metadata"
	// containerMetadataExtension is an extension name that identify metadata of container in CreateContainerRequest
	containerMetadataExtension = criContainerdPrefix + ".container.metadata"
	// imageLabelConfigDigest is the label key for image ID.
	imageLabelConfigDigest = criContainerdPrefix + ".image-config-digest"
	// imageLabelChainID is the label key for image's chain ID
	imageLabelChainID = criContainerdPrefix + ".image-chain-id"
	// imageLabelSize is the label key for image size information.
	imageLabelSize = criContainerdPrefix + ".image-size"

	// defaultIfName is the default network interface for the pods
	defaultIfName = "eth0"

	// runtimeRunhcsV1 is the runtime type for runhcs.
	runtimeRunhcsV1 = "io.containerd.runhcs.v1"
)

// makeSandboxName generates sandbox name from sandbox metadata. The name
// generated is unique as long as sandbox metadata is unique.
func makeSandboxName(s *runtime.PodSandboxMetadata) string {
	return strings.Join([]string{
		s.Name,                       // 0
		s.Namespace,                  // 1
		s.Uid,                        // 2
		fmt.Sprintf("%d", s.Attempt), // 3
	}, nameDelimiter)
}

// makeContainerName generates container name from sandbox and container metadata.
// The name generated is unique as long as the sandbox container combination is
// unique.
func makeContainerName(c *runtime.ContainerMetadata, s *runtime.PodSandboxMetadata) string {
	return strings.Join([]string{
		c.Name,                       // 0: container name
		s.Name,                       // 1: pod name
		s.Namespace,                  // 2: pod namespace
		s.Uid,                        // 3: pod uid
		fmt.Sprintf("%d", c.Attempt), // 4: attempt number of creating the container
	}, nameDelimiter)
}

// getSandboxRootDir returns the root directory for managing sandbox files,
// e.g. hosts files.
func (c *criService) getSandboxRootDir(id string) string {
	return filepath.Join(c.config.RootDir, sandboxesDir, id)
}

// getVolatileSandboxRootDir returns the root directory for managing volatile sandbox files,
// e.g. named pipes.
func (c *criService) getVolatileSandboxRootDir(id string) string {
	return filepath.Join(c.config.StateDir, sandboxesDir, id)
}

// getContainerRootDir returns the root directory for managing container files,
// e.g. state checkpoint.
func (c *criService) getContainerRootDir(id string) string {
	return filepath.Join(c.config.RootDir, containersDir, id)
}

// getVolatileContainerRootDir returns the root directory for managing volatile container files,
// e.g. named pipes.
func (c *criService) getVolatileContainerRootDir(id string) string {
	return filepath.Join(c.config.StateDir, containersDir, id)
}

// criContainerStateToString formats CRI container state to string.
func criContainerStateToString(state runtime.ContainerState) string {
	return runtime.ContainerState_name[int32(state)]
}

// getRepoDigestAngTag returns image repoDigest and repoTag of the named image reference.
func getRepoDigestAndTag(namedRef docker.Named, digest imagedigest.Digest, schema1 bool) (string, string) {
	var repoTag, repoDigest string
	if _, ok := namedRef.(docker.NamedTagged); ok {
		repoTag = namedRef.String()
	}
	if _, ok := namedRef.(docker.Canonical); ok {
		repoDigest = namedRef.String()
	} else if !schema1 {
		// digest is not actual repo digest for schema1 image.
		repoDigest = namedRef.Name() + "@" + digest.String()
	}
	return repoDigest, repoTag
}

// localResolve resolves image reference locally and returns corresponding image metadata. It
// returns store.ErrNotExist if the reference doesn't exist.
func (c *criService) localResolve(ctx context.Context, refOrID string) (containerd.Image, error) {

	var (
		filters []string
		// Query only images already updated by CRI
		managed = fmt.Sprintf(`labels."%s"=="%s"`, imageLabelKey, imageLabelValue)
	)

	if _, err := imagedigest.Parse(refOrID); err != nil {
		// Not a digest, try find by ref
		if normalized, err := docker.ParseDockerRef(refOrID); err == nil {
			filters = append(filters, fmt.Sprintf(`name@="%s",%s`, normalized, managed))
		}

		filters = append(filters, fmt.Sprintf(`name@="%s",%s`, refOrID, managed))
	} else {
		// Got a valid digest, just perform strong match
		filters = append(filters, fmt.Sprintf(`name=="%s",%s`, refOrID, managed))
	}

	list, err := c.client.ImageService().List(ctx, filters...)
	if err != nil {
		return nil, fmt.Errorf("failed to list images: %w", err)
	}

	if len(list) == 0 {
		return nil, errdefs.ErrNotFound
	}

	image := list[0]
	return c.client.GetImage(ctx, image.Name)
}

// getUserFromImage gets uid or user name of the image user.
// If user is numeric, it will be treated as uid; or else, it is treated as user name.
func getUserFromImage(user string) (*int64, string) {
	// return both empty if user is not specified in the image.
	if user == "" {
		return nil, ""
	}
	// split instances where the id may contain user:group
	user = strings.Split(user, ":")[0]
	// user could be either uid or user name. Try to interpret as numeric uid.
	uid, err := strconv.ParseInt(user, 10, 64)
	if err != nil {
		// If user is non numeric, assume it's user name.
		return nil, user
	}
	// If user is a numeric uid.
	return &uid, ""
}

// ensureImageExists returns corresponding metadata of the image reference, if image is not
// pulled yet, the function will pull the image.
func (c *criService) ensureImageExists(ctx context.Context, ref string, config *runtime.PodSandboxConfig) (containerd.Image, error) {
	image, err := c.localResolve(ctx, ref)
	if err != nil && !errdefs.IsNotFound(err) {
		return nil, fmt.Errorf("failed to get image %q: %w", ref, err)
	}
	if err == nil {
		return image, nil
	}
	// Pull image to ensure the image exists
	resp, err := c.PullImage(ctx, &runtime.PullImageRequest{Image: &runtime.ImageSpec{Image: ref}, SandboxConfig: config})
	if err != nil {
		return nil, fmt.Errorf("failed to pull image %q: %w", ref, err)
	}
	imageID := resp.GetImageRef()
	newImage, err := c.client.GetImage(ctx, imageID)
	if err != nil {
		// It's still possible that someone removed the image right after it is pulled.
		return nil, fmt.Errorf("failed to get image %q after pulling: %w", imageID, err)
	}
	return newImage, nil
}

// validateTargetContainer checks that a container is a valid
// target for a container using PID NamespaceMode_TARGET.
// The target container must be in the same sandbox and must be running.
// Returns the target container for convenience.
func (c *criService) validateTargetContainer(sandboxID, targetContainerID string) (containerstore.Container, error) {
	targetContainer, err := c.containerStore.Get(targetContainerID)
	if err != nil {
		return containerstore.Container{}, fmt.Errorf("container %q does not exist: %w", targetContainerID, err)
	}

	targetSandboxID := targetContainer.Metadata.SandboxID
	if targetSandboxID != sandboxID {
		return containerstore.Container{},
			fmt.Errorf("container %q (sandbox %s) does not belong to sandbox %s", targetContainerID, targetSandboxID, sandboxID)
	}

	status := targetContainer.Status.Get()
	if state := status.State(); state != runtime.ContainerState_CONTAINER_RUNNING {
		return containerstore.Container{}, fmt.Errorf("container %q is not running - in state %s", targetContainerID, state)
	}

	return targetContainer, nil
}

// isInCRIMounts checks whether a destination is in CRI mount list.
func isInCRIMounts(dst string, mounts []*runtime.Mount) bool {
	for _, m := range mounts {
		if filepath.Clean(m.ContainerPath) == filepath.Clean(dst) {
			return true
		}
	}
	return false
}

// filterLabel returns a label filter. Use `%q` here because containerd
// filter needs extra quote to work properly.
func filterLabel(k, v string) string {
	return fmt.Sprintf("labels.%q==%q", k, v)
}

// buildLabel builds the labels from config to be passed to containerd
func buildLabels(configLabels, imageConfigLabels map[string]string, containerType string) map[string]string {
	labels := make(map[string]string)

	for k, v := range imageConfigLabels {
		if err := clabels.Validate(k, v); err == nil {
			labels[k] = v
		} else {
			// In case the image label is invalid, we output a warning and skip adding it to the
			// container.
			logrus.WithError(err).Warnf("unable to add image label with key %s to the container", k)
		}
	}
	// labels from the CRI request (config) will override labels in the image config
	for k, v := range configLabels {
		labels[k] = v
	}
	labels[containerKindLabel] = containerType
	return labels
}

// toRuntimeAuthConfig converts cri plugin auth config to runtime auth config.
func toRuntimeAuthConfig(a criconfig.AuthConfig) *runtime.AuthConfig {
	return &runtime.AuthConfig{
		Username:      a.Username,
		Password:      a.Password,
		Auth:          a.Auth,
		IdentityToken: a.IdentityToken,
	}
}

// parseImageReferences parses a list of arbitrary image references and returns
// the repotags and repodigests
func parseImageReferences(refs []string) ([]string, []string) {
	var tags, digests []string
	for _, ref := range refs {
		parsed, err := docker.ParseAnyReference(ref)
		if err != nil {
			continue
		}
		if _, ok := parsed.(docker.Canonical); ok {
			digests = append(digests, parsed.String())
		} else if _, ok := parsed.(docker.Tagged); ok {
			tags = append(tags, parsed.String())
		}
	}
	return tags, digests
}

// generateRuntimeOptions generates runtime options from cri plugin config.
func generateRuntimeOptions(r criconfig.Runtime, c criconfig.Config) (interface{}, error) {
	if r.Options == nil {
		if r.Type != plugin.RuntimeLinuxV1 {
			return nil, nil
		}
		// This is a legacy config, generate runctypes.RuncOptions.
		return &runctypes.RuncOptions{
			Runtime:       r.Engine,
			RuntimeRoot:   r.Root,
			SystemdCgroup: c.SystemdCgroup,
		}, nil
	}
	optionsTree, err := toml.TreeFromMap(r.Options)
	if err != nil {
		return nil, err
	}
	options := getRuntimeOptionsType(r.Type)
	if err := optionsTree.Unmarshal(options); err != nil {
		return nil, err
	}
	return options, nil
}

// getRuntimeOptionsType gets empty runtime options by the runtime type name.
func getRuntimeOptionsType(t string) interface{} {
	switch t {
	case plugin.RuntimeRuncV1:
		fallthrough
	case plugin.RuntimeRuncV2:
		return &runcoptions.Options{}
	case plugin.RuntimeLinuxV1:
		return &runctypes.RuncOptions{}
	case runtimeRunhcsV1:
		return &runhcsoptions.Options{}
	default:
		return &runtimeoptions.Options{}
	}
}

// getRuntimeOptions get runtime options from container metadata.
func getRuntimeOptions(c containers.Container) (interface{}, error) {
	from := c.Runtime.Options
	if from == nil || from.GetValue() == nil {
		return nil, nil
	}
	opts, err := typeurl.UnmarshalAny(from)
	if err != nil {
		return nil, err
	}
	return opts, nil
}

const (
	// unknownExitCode is the exit code when exit reason is unknown.
	unknownExitCode = 255
	// unknownExitReason is the exit reason when exit reason is unknown.
	unknownExitReason = "Unknown"
)

// unknownContainerStatus returns the default container status when its status is unknown.
func unknownContainerStatus() containerstore.Status {
	return containerstore.Status{
		CreatedAt:  0,
		StartedAt:  0,
		FinishedAt: 0,
		ExitCode:   unknownExitCode,
		Reason:     unknownExitReason,
		Unknown:    true,
	}
}

// unknownSandboxStatus returns the default sandbox status when its status is unknown.
func unknownSandboxStatus() sandboxstore.Status {
	return sandboxstore.Status{
		State: sandboxstore.StateUnknown,
	}
}

// getPassthroughAnnotations filters requested pod annotations by comparing
// against permitted annotations for the given runtime.
func getPassthroughAnnotations(podAnnotations map[string]string,
	runtimePodAnnotations []string) (passthroughAnnotations map[string]string) {
	passthroughAnnotations = make(map[string]string)

	for podAnnotationKey, podAnnotationValue := range podAnnotations {
		for _, pattern := range runtimePodAnnotations {
			// Use path.Match instead of filepath.Match here.
			// filepath.Match treated `\\` as path separator
			// on windows, which is not what we want.
			if ok, _ := path.Match(pattern, podAnnotationKey); ok {
				passthroughAnnotations[podAnnotationKey] = podAnnotationValue
			}
		}
	}
	return passthroughAnnotations
}

func retrieveImageSpec(ctx context.Context, image containerd.Image) (imagespec.Image, error) {
	spec, err := image.Spec(ctx)
	if err != nil {
		return imagespec.Image{}, fmt.Errorf("failed to get image spec for image %q: %w", image.Name(), err)
	}

	return spec, nil
}

// getImageSpec retrieves an image spec from containerd client.
// This declared as variable for easier unit testing.
var getImageSpec = retrieveImageSpec

// findReferences retrieves all image references from metadata store.
func (c *criService) findReferences(ctx context.Context, imageID string) ([]string, error) {
	filter := fmt.Sprintf(`labels."%s"=="%s"`, imageLabelConfigDigest, imageID)
	list, err := c.client.ImageService().List(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to query image store: %w", err)
	}

	var references []string
	for _, image := range list {
		references = append(references, image.Name)
	}

	return references, nil
}

// getImageID retrieves image ID from image labels.
func getImageID(image containerd.Image) (string, error) {
	if labels := image.Labels(); labels != nil {
		if id, ok := labels[imageLabelConfigDigest]; ok {
			return id, nil
		}
	}
	return "", fmt.Errorf("image %q has no ID label", image.Name())
}

// ensureImageMetadata will make sure image gets all metadata labels required by CRI.
func (c *criService) ensureImageMetadata(ctx context.Context, name string) error {
	image, err := c.client.GetImage(ctx, name)
	if err != nil {
		return fmt.Errorf("unable to get image %q: %w", name, err)
	}

	var (
		metadata = image.Metadata()
		update   = false
	)

	if metadata.Labels == nil {
		metadata.Labels = make(map[string]string)
	}

	if _, ok := metadata.Labels[imageLabelConfigDigest]; !ok {
		imageConfig, err := image.Config(ctx)
		if err != nil {
			return fmt.Errorf("failed to get config descriptor: %w", err)
		}

		metadata.Labels[imageLabelConfigDigest] = imageConfig.Digest.String()
		update = true
	}

	if _, ok := metadata.Labels[imageLabelChainID]; !ok {
		diffIDs, err := image.RootFS(ctx)
		if err != nil {
			return fmt.Errorf("failed to get diffids: %w", err)
		}

		chainID := imageidentity.ChainID(diffIDs)
		metadata.Labels[imageLabelChainID] = chainID.String()
		update = true
	}

	if _, ok := metadata.Labels[imageLabelSize]; !ok {
		size, err := image.Size(ctx)
		if err != nil {
			return fmt.Errorf("get image compressed resource size: %w", err)
		}

		metadata.Labels[imageLabelSize] = strconv.FormatInt(size, 10)
		update = true
	}

	if update {
		metadata.Labels[imageLabelKey] = imageLabelValue

		if _, err := c.client.ImageService().Update(ctx, metadata, "labels"); err != nil {
			return fmt.Errorf("failed to update image metadata with repo tag and digest: %w", err)
		}
	}

	return nil
}
