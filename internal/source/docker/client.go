package docker

import (
	"context"
	"io"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/client"
)

// SDKClient adapts the official Docker Go SDK to the deliberately narrow API
// used by LogSource. It exposes no mutating container operations.
type SDKClient struct {
	client *client.Client
}

func NewSDKClient(host string) (*SDKClient, error) {
	api, err := client.NewClientWithOpts(client.WithHost(host), client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &SDKClient{client: api}, nil
}

func (c *SDKClient) Close() error { return c.client.Close() }

func (c *SDKClient) List(ctx context.Context) ([]Container, error) {
	items, err := c.client.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}
	result := make([]Container, 0, len(items))
	for _, item := range items {
		result = append(result, Container{ID: item.ID, Names: item.Names, Running: item.State == "running"})
	}
	return result, nil
}

func (c *SDKClient) Inspect(ctx context.Context, id string) (Container, error) {
	item, err := c.client.ContainerInspect(ctx, id)
	if err != nil {
		return Container{}, err
	}
	running := item.State != nil && item.State.Running
	tty := item.Config != nil && item.Config.Tty
	name := ""
	if item.Name != "" {
		name = item.Name
	}
	return Container{ID: item.ID, Names: []string{name}, Running: running, TTY: tty}, nil
}

func (c *SDKClient) Logs(ctx context.Context, id string, since time.Time) (io.ReadCloser, error) {
	return c.client.ContainerLogs(ctx, id, container.LogsOptions{ShowStdout: true, ShowStderr: true, Follow: true, Timestamps: true, Since: since.Format(time.RFC3339Nano)})
}

func (c *SDKClient) Events(ctx context.Context) (<-chan Event, <-chan error) {
	messages, errors := c.client.Events(ctx, events.ListOptions{})
	output := make(chan Event)
	go func() {
		defer close(output)
		for message := range messages {
			if message.Type != "container" {
				continue
			}
			select {
			case output <- Event{Action: string(message.Action), Name: message.Actor.Attributes["name"], ID: message.Actor.ID}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return output, errors
}
