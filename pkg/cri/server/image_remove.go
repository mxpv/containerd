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
	"errors"
	"fmt"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"

	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// RemoveImage removes the image.
// TODO(random-liu): Update CRI to pass image reference instead of ImageSpec. (See
// kubernetes/kubernetes#46255)
// TODO(random-liu): We should change CRI to distinguish image id and image spec.
// Remove the whole image no matter the it's image id or reference. This is the
// semantic defined in CRI now.
func (c *criService) RemoveImage(ctx context.Context, r *runtime.RemoveImageRequest) (*runtime.RemoveImageResponse, error) {
	image, err := c.localResolve(ctx, r.GetImage().GetImage())
	if err != nil {
		if errdefs.IsNotFound(err) {
			// return empty without error when image not found.
			return &runtime.RemoveImageResponse{}, nil
		}
		return nil, fmt.Errorf("can not resolve %q locally: %w", r.GetImage().GetImage(), err)
	}

	// Remove all image references created in CRI implementations up to 1.7
	var (
		labels     = image.Labels()
		references []string
	)

	if ref, ok := labels[containerd.ImageLabelConfigDigest]; ok {
		references = append(references, ref)
	}

	if ref, ok := labels[imageLabelRepoTag]; ok {
		references = append(references, ref)
	}

	if ref, ok := labels[imageLabelRepoDigest]; ok {
		references = append(references, ref)
	}

	for _, ref := range references {
		if err := c.client.ImageService().Delete(ctx, ref); err != nil && !errors.Is(err, errdefs.ErrNotFound) {
			return nil, fmt.Errorf("failed to delete image reference %q for %q: %w", ref, image.Name(), err)
		}
	}

	// Delete the last image reference synchronously to trigger garbage collection.
	// This is best effort. It is possible that the image reference is deleted by
	// someone else before this point.
	opts := []images.DeleteOpt{images.SynchronousDelete()}

	err = c.client.ImageService().Delete(ctx, image.Name(), opts...)
	if err != nil && !errors.Is(err, errdefs.ErrNotFound) {
		return nil, fmt.Errorf("failed to delete image from store: %w", err)
	}

	return &runtime.RemoveImageResponse{}, nil
}
