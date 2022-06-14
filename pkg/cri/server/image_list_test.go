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
	"testing"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/metadata"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestListImages(t *testing.T) {
	metadataStore := []images.Image{
		// Image 1
		{
			Name:   "sha256:1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			Target: fakeTarget,
			Labels: map[string]string{
				imageLabelConfigDigest: "sha256:1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				imageLabelSize:         "1000",
				imageLabelKey:          imageLabelValue,
			},
		},
		{
			Name:   "gcr.io/library/busybox:latest",
			Target: fakeTarget,
			Labels: map[string]string{
				imageLabelConfigDigest: "sha256:1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				imageLabelSize:         "1000",
				imageLabelKey:          imageLabelValue,
			},
		},
		{
			Name:   "gcr.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
			Target: fakeTarget,
			Labels: map[string]string{
				imageLabelConfigDigest: "sha256:1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				imageLabelSize:         "1000",
				imageLabelKey:          imageLabelValue,
			},
		},
		// Image 2
		{
			Name:   "sha256:2123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			Target: fakeTarget,
			Labels: map[string]string{
				imageLabelConfigDigest: "sha256:2123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				imageLabelSize:         "2000",
				imageLabelKey:          imageLabelValue,
			},
		},
		{
			Name:   "gcr.io/library/alpine:latest",
			Target: fakeTarget,
			Labels: map[string]string{
				imageLabelConfigDigest: "sha256:2123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				imageLabelSize:         "2000",
				imageLabelKey:          imageLabelValue,
			},
		},
		{
			Name:   "gcr.io/library/alpine@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
			Target: fakeTarget,
			Labels: map[string]string{
				imageLabelConfigDigest: "sha256:2123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				imageLabelSize:         "2000",
				imageLabelKey:          imageLabelValue,
			},
		},
		// Image 3
		{
			Name:   "sha256:3123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			Target: fakeTarget,
			Labels: map[string]string{
				imageLabelConfigDigest: "sha256:3123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				imageLabelSize:         "3000",
				imageLabelKey:          imageLabelValue,
			},
		},
		{
			Name:   "gcr.io/library/ubuntu:latest",
			Target: fakeTarget,
			Labels: map[string]string{
				imageLabelConfigDigest: "sha256:3123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				imageLabelSize:         "3000",
				imageLabelKey:          imageLabelValue,
			},
		}, {
			Name:   "gcr.io/library/ubuntu@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
			Target: fakeTarget,
			Labels: map[string]string{
				imageLabelConfigDigest: "sha256:3123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				imageLabelSize:         "3000",
				imageLabelKey:          imageLabelValue,
			},
		},
	}

	getImageSpec = func(ctx context.Context, image containerd.Image) (imagespec.Image, error) {
		switch image.Name() {
		case "sha256:1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "gcr.io/library/busybox:latest":
			return imagespec.Image{Config: imagespec.ImageConfig{User: "root"}}, nil
		case "sha256:2123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "gcr.io/library/alpine:latest":
			return imagespec.Image{Config: imagespec.ImageConfig{User: "1234:1234"}}, nil
		case "sha256:3123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "gcr.io/library/ubuntu:latest":
			return imagespec.Image{Config: imagespec.ImageConfig{User: "nobody"}}, nil
		default:
			t.Fatalf("unexpected OCI spec request for image %q", image.Name())
		}
		return imagespec.Image{}, nil
	}
	t.Cleanup(func() { getImageSpec = retrieveImageSpec })

	expect := []*runtime.Image{
		{
			Id:          "sha256:1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			RepoTags:    []string{"gcr.io/library/busybox:latest"},
			RepoDigests: []string{"gcr.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582"},
			Size_:       uint64(1000),
			Username:    "root",
		},
		{
			Id:          "sha256:2123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			RepoTags:    []string{"gcr.io/library/alpine:latest"},
			RepoDigests: []string{"gcr.io/library/alpine@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582"},
			Size_:       uint64(2000),
			Uid:         &runtime.Int64Value{Value: 1234},
		},
		{
			Id:          "sha256:3123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			RepoTags:    []string{"gcr.io/library/ubuntu:latest"},
			RepoDigests: []string{"gcr.io/library/ubuntu@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582"},
			Size_:       uint64(3000),
			Username:    "nobody",
		},
	}

	var (
		ctx, db    = makeTestDB(t)
		imageStore = metadata.NewImageStore(db)
		c          = newTestCRIService(containerd.WithImageStore(imageStore))
	)

	for _, img := range metadataStore {
		_, err := imageStore.Create(ctx, img)
		require.NoError(t, err)
	}

	resp, err := c.ListImages(ctx, &runtime.ListImagesRequest{})
	assert.NoError(t, err)
	require.NotNil(t, resp)
	images := resp.GetImages()
	assert.Len(t, images, len(expect))
	for _, i := range expect {
		assert.Contains(t, images, i)
	}
}
