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
	"testing"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/metadata"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestListImages(t *testing.T) {
	fakeTarget := ocispec.Descriptor{
		MediaType: "test",
		Digest:    "sha256:c75bebcdd211f41b3a460c7bf82970ed6c75acaab9cd4c9a4e125b03ca113799",
		Size:      100000,
	}

	imagesInStore := []images.Image{
		{
			Name: "image-1",
			Labels: map[string]string{
				containerd.ImageLabelConfigDigest: "sha256:1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				imageLabelChainID:                 "test-chainid-1",
				imageLabelRepoTag:                 "gcr.io/library/busybox:latest",
				imageLabelRepoDigest:              "gcr.io/library/busybox@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
				imageLabelSize:                    "1000",
				imageLabelSpec:                    `{"config":{"User":"root"}}`,
			},
			Target: fakeTarget,
		},
		{
			Name: "image-2",
			Labels: map[string]string{
				containerd.ImageLabelConfigDigest: "sha256:2123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				imageLabelChainID:                 "test-chainid-2",
				imageLabelRepoTag:                 "gcr.io/library/alpine:latest",
				imageLabelRepoDigest:              "gcr.io/library/alpine@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
				imageLabelSize:                    "2000",
				imageLabelSpec:                    `{"config":{"User":"1234:1234"}}`,
			},
			Target: fakeTarget,
		},
		{
			Name: "image-3",
			Labels: map[string]string{
				containerd.ImageLabelConfigDigest: "sha256:3123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				imageLabelChainID:                 "test-chainid-3",
				imageLabelRepoTag:                 "gcr.io/library/ubuntu:latest",
				imageLabelRepoDigest:              "gcr.io/library/ubuntu@sha256:e6693c20186f837fc393390135d8a598a96a833917917789d63766cab6c59582",
				imageLabelSize:                    "3000",
				imageLabelSpec:                    `{"config":{"User":"nobody"}}`,
			},
			Target: fakeTarget,
		},
	}
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

	for _, img := range imagesInStore {
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
