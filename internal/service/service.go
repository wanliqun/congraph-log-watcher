// Package service coordinates runtime component startup and graceful shutdown.
package service

import (
	"context"
	"fmt"
	"sync"
)

type Component interface {
	Start(context.Context) error
	Close(context.Context) error
}
type Runtime struct{ components []Component }

func New(components ...Component) *Runtime { return &Runtime{components: components} }
func (r *Runtime) Run(ctx context.Context) error {
	started := []Component{}
	for _, c := range r.components {
		if err := c.Start(ctx); err != nil {
			_ = closeAll(ctx, started)
			return fmt.Errorf("start component: %w", err)
		}
		started = append(started, c)
	}
	<-ctx.Done()
	return closeAll(ctx, started)
}
func closeAll(ctx context.Context, values []Component) error {
	var once sync.Once
	var result error
	for i := len(values) - 1; i >= 0; i-- {
		if err := values[i].Close(ctx); err != nil {
			once.Do(func() { result = err })
		}
	}
	return result
}
