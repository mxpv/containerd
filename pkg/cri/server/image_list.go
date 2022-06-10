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

	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// ListImages lists existing images.
// TODO(random-liu): Add image list filters after CRI defines this more clear, and kubelet
// actually needs it.
func (c *criService) ListImages(ctx context.Context, r *runtime.ListImagesRequest) (*runtime.ListImagesResponse, error) {
	list, err := c.client.ListImages(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query image list from metadata store: %w", err)
	}

	var images []*runtime.Image
	for _, image := range list {
		spec, err := image.Spec(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get image spec for image %q: %w", err)
		}

		image, err := toCRIImage(image, spec)
		if err != nil {
			return nil, err
		}

		// TODO(random-liu): [P0] Make sure corresponding snapshot exists. What if snapshot
		// doesn't exist?
		images = append(images, image)
	}

	return &runtime.ListImagesResponse{Images: images}, nil
}
