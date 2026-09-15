// Package docker implements a read-only Docker Engine log source.
package docker

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/wanliqun/congraph-log-watcher/internal/logentry"
)

// Container is the minimal container identity required by LogSource.
type Container struct {
	ID      string
	Names   []string
	Running bool
	TTY     bool
}

// Event represents the Docker lifecycle events relevant to a log stream.
type Event struct {
	Action string
	Name   string
	ID     string
}

// API is intentionally limited to the read-only Docker operations used by the
// watcher. It makes source behavior testable without a Docker daemon.
type API interface {
	List(context.Context) ([]Container, error)
	Inspect(context.Context, string) (Container, error)
	Logs(context.Context, string, time.Time) (io.ReadCloser, error)
	Events(context.Context) (<-chan Event, <-chan error)
}

// Config configures one shared-output Docker LogSource.
type Config struct {
	API            API
	Containers     []string
	Output         chan<- logentry.RawLog
	ReplayStarts   map[string]time.Time
	ReplayStart    func(string) time.Time
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	Observer       Observer
}

// Observer receives bounded operational state changes. Implementations must
// return quickly; callbacks run on source goroutines.
type Observer interface {
	DockerAvailable()
	ContainerAttached(string, string, bool)
	ContainerReconnect(string)
}

// LogSource reads each target container in its own goroutine. Sending to
// Output is deliberately blocking, providing bounded-channel backpressure.
type LogSource struct {
	api          API
	containers   []string
	output       chan<- logentry.RawLog
	replayStarts map[string]time.Time
	replayStart  func(string) time.Time
	initial      time.Duration
	max          time.Duration
	observer     Observer

	mu    sync.Mutex
	wakes map[string]chan struct{}
}

// New validates a Docker log source.
func New(config Config) (*LogSource, error) {
	if config.API == nil {
		return nil, fmt.Errorf("docker API must not be nil")
	}
	if len(config.Containers) == 0 {
		return nil, fmt.Errorf("at least one container is required")
	}
	if config.Output == nil {
		return nil, fmt.Errorf("output channel must not be nil")
	}
	if config.InitialBackoff <= 0 {
		config.InitialBackoff = 100 * time.Millisecond
	}
	if config.MaxBackoff <= 0 {
		config.MaxBackoff = 5 * time.Second
	}
	if config.MaxBackoff < config.InitialBackoff {
		return nil, fmt.Errorf("max backoff must not be less than initial backoff")
	}
	starts := make(map[string]time.Time, len(config.ReplayStarts))
	for name, start := range config.ReplayStarts {
		starts[name] = start
	}
	wakes := make(map[string]chan struct{}, len(config.Containers))
	for _, name := range config.Containers {
		name = normalizeName(name)
		if name == "" {
			return nil, fmt.Errorf("container name must not be empty")
		}
		if _, exists := wakes[name]; exists {
			return nil, fmt.Errorf("duplicate container name %q", name)
		}
		wakes[name] = make(chan struct{}, 1)
	}
	return &LogSource{api: config.API, containers: normalizedNames(config.Containers), output: config.Output, replayStarts: starts, replayStart: config.ReplayStart, initial: config.InitialBackoff, max: config.MaxBackoff, observer: config.Observer, wakes: wakes}, nil
}

// Run starts lifecycle watching and one sequential stream manager per logical
// container. It returns only after context cancellation and never closes the
// caller-owned Output channel.
func (s *LogSource) Run(ctx context.Context) error {
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		s.watchEvents(ctx)
	}()
	for _, name := range s.containers {
		workers.Add(1)
		go func(name string) {
			defer workers.Done()
			s.runContainer(ctx, name)
		}(name)
	}
	workers.Wait()
	return ctx.Err()
}

func (s *LogSource) runContainer(ctx context.Context, name string) {
	backoff := s.initial
	attachedOnce := false
	for ctx.Err() == nil {
		container, err := s.discover(ctx, name)
		if err != nil {
			s.observeAttached(name, "", false)
			if attachedOnce {
				s.observeReconnect(name)
			}
			if !wait(ctx, s.wakes[name], backoff) {
				return
			}
			backoff = nextBackoff(backoff, s.max)
			continue
		}
		backoff = s.initial
		attachedOnce = true
		s.observeAttached(name, container.ID, true)
		streamCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() {
			done <- s.stream(streamCtx, name, container)
		}()
		select {
		case <-ctx.Done():
			cancel()
			<-done
			return
		case <-s.wakes[name]:
			// A lifecycle event may indicate a recreated container; force a
			// fresh list/inspect before continuing the old stream.
			cancel()
			<-done
			s.observeAttached(name, container.ID, false)
			s.observeReconnect(name)
		case <-done:
			// A closed log follow stream is normally a daemon or container
			// transition. Back off before rediscovery to avoid a tight loop.
			cancel()
			s.observeAttached(name, container.ID, false)
			s.observeReconnect(name)
			if !wait(ctx, s.wakes[name], backoff) {
				return
			}
			backoff = nextBackoff(backoff, s.max)
		}
	}
}

func (s *LogSource) discover(ctx context.Context, name string) (Container, error) {
	containers, err := s.api.List(ctx)
	if err != nil {
		return Container{}, err
	}
	if s.observer != nil {
		s.observer.DockerAvailable()
	}
	for _, item := range containers {
		if !hasName(item.Names, name) {
			continue
		}
		inspected, err := s.api.Inspect(ctx, item.ID)
		if err != nil {
			return Container{}, err
		}
		if !inspected.Running {
			return Container{}, fmt.Errorf("container %q is not running", name)
		}
		return inspected, nil
	}
	return Container{}, fmt.Errorf("container %q not found", name)
}

func (s *LogSource) observeAttached(name, id string, attached bool) {
	if s.observer != nil {
		s.observer.ContainerAttached(name, id, attached)
	}
}

func (s *LogSource) observeReconnect(name string) {
	if s.observer != nil {
		s.observer.ContainerReconnect(name)
	}
}

func (s *LogSource) stream(ctx context.Context, name string, container Container) error {
	start := s.replayStarts[name]
	if s.replayStart != nil {
		start = s.replayStart(name)
	}
	reader, err := s.api.Logs(ctx, container.ID, start)
	if err != nil {
		return err
	}
	defer reader.Close()
	if container.TTY {
		return decodePlain(ctx, reader, name, container.ID, s.output)
	}
	return decodeMultiplexed(ctx, reader, name, container.ID, s.output)
}

func (s *LogSource) watchEvents(ctx context.Context) {
	backoff := s.initial
	for ctx.Err() == nil {
		events, errors := s.api.Events(ctx)
		for events != nil || errors != nil {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-events:
				if !ok {
					events = nil
					continue
				}
				if isLifecycleAction(event.Action) {
					s.wake(normalizeName(event.Name))
				}
			case _, ok := <-errors:
				if !ok {
					errors = nil
				}
			}
		}
		if !wait(ctx, nil, backoff) {
			return
		}
		backoff = nextBackoff(backoff, s.max)
	}
}

func (s *LogSource) wake(name string) {
	s.mu.Lock()
	wake := s.wakes[name]
	s.mu.Unlock()
	if wake == nil {
		return
	}
	select {
	case wake <- struct{}{}:
	default:
	}
}

func decodePlain(ctx context.Context, reader io.Reader, name, id string, output chan<- logentry.RawLog) error {
	return readLines(reader, func(line string) error {
		return emit(ctx, output, name, id, "stdout", line)
	})
}

func decodeMultiplexed(ctx context.Context, reader io.Reader, name, id string, output chan<- logentry.RawLog) error {
	var header [8]byte
	buffers := map[byte]string{1: "", 2: ""}
	for {
		if _, err := io.ReadFull(reader, header[:]); err != nil {
			if err == io.EOF {
				for stream, buffered := range buffers {
					if buffered != "" {
						if err := emit(ctx, output, name, id, streamName(stream), buffered); err != nil {
							return err
						}
					}
				}
				return nil
			}
			return err
		}
		stream := header[0]
		length := binary.BigEndian.Uint32(header[4:])
		payload := make([]byte, length)
		if _, err := io.ReadFull(reader, payload); err != nil {
			return err
		}
		if stream != 1 && stream != 2 {
			continue
		}
		buffers[stream] += string(payload)
		for {
			index := strings.IndexByte(buffers[stream], '\n')
			if index < 0 {
				break
			}
			line := strings.TrimSuffix(buffers[stream][:index], "\r")
			buffers[stream] = buffers[stream][index+1:]
			if err := emit(ctx, output, name, id, streamName(stream), line); err != nil {
				return err
			}
		}
	}
}

func readLines(reader io.Reader, consume func(string) error) error {
	buffered := bufio.NewReader(reader)
	for {
		line, err := buffered.ReadString('\n')
		if line != "" {
			line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
			if consumeErr := consume(line); consumeErr != nil {
				return consumeErr
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func emit(ctx context.Context, output chan<- logentry.RawLog, name, id, stream, line string) error {
	timestamp, raw := parseTimestamp(line)
	select {
	case output <- logentry.RawLog{ContainerID: id, ContainerName: name, Timestamp: timestamp, Stream: stream, Raw: raw}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func parseTimestamp(line string) (time.Time, string) {
	value, rest, found := strings.Cut(line, " ")
	if !found {
		return time.Time{}, line
	}
	timestamp, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, line
	}
	return timestamp, rest
}

func normalizeName(value string) string { return strings.TrimPrefix(strings.TrimSpace(value), "/") }

func normalizedNames(values []string) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = normalizeName(value)
	}
	return result
}

func hasName(names []string, wanted string) bool {
	for _, name := range names {
		if normalizeName(name) == wanted {
			return true
		}
	}
	return false
}

func isLifecycleAction(action string) bool {
	switch action {
	case "create", "start", "die", "destroy":
		return true
	default:
		return false
	}
}

func streamName(stream byte) string {
	if stream == 2 {
		return "stderr"
	}
	return "stdout"
}

func nextBackoff(current, max time.Duration) time.Duration {
	if current >= max/2 {
		return max
	}
	return current * 2
}

func wait(ctx context.Context, wake <-chan struct{}, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	case <-wake:
		return true
	}
}
