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

package containerd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/containerd/typeurl"
	"github.com/hashicorp/go-multierror"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/containerd/protobuf"
	"github.com/containerd/containerd/protobuf/types"
	api "github.com/containerd/containerd/sandbox"
)

// Sandbox is a high level client to containerd's sandboxes.
type Sandbox interface {
	// ID is a sandbox identifier
	ID() string
	// Metadata provides access to the underlying sandbox's metadata object.
	Metadata() api.Sandbox
	// NewContainer creates new container that will belong to this sandbox
	NewContainer(ctx context.Context, id string, opts ...NewContainerOpts) (Container, error)
	// Labels returns the labels set on the sandbox
	Labels(ctx context.Context) (map[string]string, error)
	// Start starts new sandbox instance
	Start(ctx context.Context) (uint32, error)
	// Stop sends stop request to the shim instance.
	Stop(ctx context.Context) error
	// Wait blocks until sandbox process exits.
	Wait(ctx context.Context) (<-chan ExitStatus, error)
	// Shutdown removes sandbox from the metadata store and shutdowns shim instance.
	Shutdown(ctx context.Context) error
}

type sandboxClient struct {
	client     *Client
	metadata   api.Sandbox
	controller api.Controller
}

func (s *sandboxClient) ID() string {
	return s.metadata.ID
}

func (s *sandboxClient) Metadata() api.Sandbox {
	return s.metadata
}

func (s *sandboxClient) NewContainer(ctx context.Context, id string, opts ...NewContainerOpts) (Container, error) {
	return s.client.NewContainer(ctx, id, append(opts, WithSandbox(s.ID()))...)
}

func (s *sandboxClient) Labels(ctx context.Context) (map[string]string, error) {
	sandbox, err := s.client.SandboxStore().Get(ctx, s.ID())
	if err != nil {
		return nil, err
	}

	return sandbox.Labels, nil
}

func (s *sandboxClient) Start(ctx context.Context) (uint32, error) {
	resp, err := s.controller.Start(ctx, s.ID())
	if err != nil {
		return 0, err
	}

	return resp.Pid, nil
}

func (s *sandboxClient) Wait(ctx context.Context) (<-chan ExitStatus, error) {
	c := make(chan ExitStatus, 1)
	go func() {
		defer close(c)

		resp, err := s.controller.Wait(ctx, s.ID())
		if err != nil {
			c <- ExitStatus{
				code: UnknownExitStatus,
				err:  err,
			}
			return
		}

		c <- ExitStatus{
			code:     resp.ExitStatus,
			exitedAt: protobuf.FromTimestamp(resp.ExitedAt),
		}
	}()

	return c, nil
}

func (s *sandboxClient) Stop(ctx context.Context) error {
	if _, err := s.controller.Stop(ctx, s.ID()); err != nil {
		return err
	}
	return nil
}

func (s *sandboxClient) Shutdown(ctx context.Context) error {
	var (
		id     = s.ID()
		result *multierror.Error
	)

	if s.controller != nil {
		if _, err := s.controller.Shutdown(ctx, id); err != nil {
			result = multierror.Append(result, fmt.Errorf("failed to shutdown sandbox %q: %w", id, err))
		}
	}

	if err := s.client.SandboxStore().Delete(ctx, s.ID()); err != nil {
		result = multierror.Append(result, fmt.Errorf("failed to delete sandbox %q from store: %w", id, err))
	}

	return result.ErrorOrNil()
}

// NewSandbox creates new sandbox client
func (c *Client) NewSandbox(ctx context.Context, sandboxID string, opts ...NewSandboxOpts) (Sandbox, error) {
	if sandboxID == "" {
		return nil, errors.New("sandbox ID must be specified")
	}

	var (
		err    error
		client = sandboxClient{client: c}
	)

	newSandbox := api.Sandbox{
		ID:        sandboxID,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	for _, opt := range opts {
		if err := opt(ctx, &client, &newSandbox); err != nil {
			return nil, err
		}
	}

	// Create metadata
	client.metadata, err = c.SandboxStore().Create(ctx, newSandbox)
	if err != nil {
		return nil, err
	}

	if client.controller == nil {
		client.controller = c.SandboxController()
	}

	// Create sandbox instance
	if err := client.controller.Create(ctx, sandboxID); err != nil {
		return nil, fmt.Errorf("failed to create sandbox %q instance: %w", sandboxID, err)
	}

	return &client, nil
}

// LoadSandbox laods existing sandbox metadata object using the id
func (c *Client) LoadSandbox(ctx context.Context, id string) (Sandbox, error) {
	sandbox, err := c.SandboxStore().Get(ctx, id)
	if err != nil {
		return nil, err
	}

	// Make sure sandbox we load is alive.
	if _, err := c.SandboxController().Status(ctx, id, false); err != nil {
		return nil, fmt.Errorf("failed to load sandbox %s, status request failed: %w", id, err)
	}

	return &sandboxClient{
		client:     c,
		metadata:   sandbox,
		controller: c.SandboxController(),
	}, nil
}

// NewSandboxOpts is a sandbox options and extensions to be provided by client
type NewSandboxOpts func(ctx context.Context, client *sandboxClient, sandbox *api.Sandbox) error

// WithSandboxRuntime allows a user to specify the runtime to be used to run a sandbox
func WithSandboxRuntime(name string, options interface{}) NewSandboxOpts {
	return func(ctx context.Context, client *sandboxClient, s *api.Sandbox) error {
		if options == nil {
			options = &types.Empty{}
		}

		opts, err := typeurl.MarshalAny(options)
		if err != nil {
			return fmt.Errorf("failed to marshal sandbox runtime options: %w", err)
		}

		s.Runtime = api.RuntimeOpts{
			Name:    name,
			Options: opts,
		}

		return nil
	}
}

// WithSandboxSpec will provide the sandbox runtime spec
func WithSandboxSpec(s *oci.Spec, opts ...oci.SpecOpts) NewSandboxOpts {
	return func(ctx context.Context, client *sandboxClient, sandbox *api.Sandbox) error {
		c := &containers.Container{ID: sandbox.ID}

		if err := oci.ApplyOpts(ctx, client.client, c, s, opts...); err != nil {
			return err
		}

		spec, err := typeurl.MarshalAny(s)
		if err != nil {
			return fmt.Errorf("failed to marshal spec: %w", err)
		}

		sandbox.Spec = spec
		return nil
	}
}

// WithSandboxExtension attaches an extension to sandbox
func WithSandboxExtension(name string, ext interface{}) NewSandboxOpts {
	return func(ctx context.Context, client *sandboxClient, s *api.Sandbox) error {
		if s.Extensions == nil {
			s.Extensions = make(map[string]typeurl.Any)
		}

		any, err := typeurl.MarshalAny(ext)
		if err != nil {
			return fmt.Errorf("failed to marshal sandbox extension: %w", err)
		}

		s.Extensions[name] = any
		return err
	}
}

// WithSandboxLabel adds a new label to sandbox's metadata.
func WithSandboxLabel(key string, value string) NewSandboxOpts {
	return func(ctx context.Context, client *sandboxClient, sandbox *api.Sandbox) error {
		if key == "" {
			return errors.New("sandbox label key must not be empty")
		}

		if sandbox.Labels == nil {
			sandbox.Labels = map[string]string{}
		}

		sandbox.Labels[key] = value
		return nil
	}
}

// WithCustomSandboxController injects a custom controller interface.
// This is used by CRI to conditionally select either podsandbox controller or remote one.
func WithCustomSandboxController(controller api.Controller) NewSandboxOpts {
	return func(ctx context.Context, client *sandboxClient, sandbox *api.Sandbox) error {
		client.controller = controller
		return nil
	}
}
