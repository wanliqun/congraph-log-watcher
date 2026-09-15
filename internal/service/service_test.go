package service

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type fake struct {
	name     string
	order    *[]string
	startErr error
}

func (f fake) Start(context.Context) error {
	*f.order = append(*f.order, "start:"+f.name)
	return f.startErr
}
func (f fake) Close(context.Context) error { *f.order = append(*f.order, "close:"+f.name); return nil }
func TestStartFailureRollsBack(t *testing.T) {
	order := []string{}
	r := New(fake{"a", &order, nil}, fake{"b", &order, errors.New("x")})
	if r.Run(context.Background()) == nil {
		t.Fatal("want error")
	}
	want := []string{"start:a", "start:b", "close:a"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("%v", order)
	}
}
