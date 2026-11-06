package pmfi

import (
	"context"
	"fmt"
	"testing"

	"github.com/Shougo/goda-fi.vim/fi"
	"github.com/Shougo/goda.vim/sdk"
)

// host has the state of goda-pm, and records the notifications.
type host struct {
	sdk.Host
	notified []string
}

func (h *host) Eval(context.Context, string) (any, error) {
	return []any{
		map[string]any{"name": "foo.vim", "path": "/r/foo.vim", "url": "https://github.com/a/foo.vim",
			"description": "Foo"},
		map[string]any{"name": "bar.vim", "path": "/r/bar.vim"},
	}, nil
}

func (h *host) Notify(_ context.Context, fn string, args ...any) error {
	h.notified = append(h.notified, fmt.Sprint(fn, args))
	return nil
}

func TestSource(t *testing.T) {
	h := &host{}
	var items []fi.Item
	err := New().Gather(context.Background(), h, fi.GatherArgs{SourceParams: fi.Params{}}, func(it []fi.Item) error {
		items = append(items, it...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Word != "bar.vim" || items[1].Word != "foo.vim : Foo" ||
		items[1].Info[0].Text != "/r/foo.vim" {
		t.Fatalf("items %+v", items)
	}
	d, err := data(items)
	if err != nil || d[1].Path != "/r/foo.vim" || !d[1].IsDirectory || d[1].URL != "https://github.com/a/foo.vim" {
		t.Errorf("data %+v %v", d, err)
	}

	items = nil
	err = New().Gather(context.Background(), h, fi.GatherArgs{SourceParams: fi.Params{"names": []any{"foo.vim"}}},
		func(it []fi.Item) error {
			items = append(items, it...)
			return nil
		})
	if err != nil || len(items) != 1 {
		t.Fatalf("names: %+v %v", items, err)
	}

	if _, err := New().Actions()["update"].Run(context.Background(), h, fi.ActionArgs{Items: items}); err != nil {
		t.Fatal(err)
	}
	if len(h.notified) != 1 || h.notified[0] != "goda_pm#async_ext_action[installer update map[names:[foo.vim]]]" {
		t.Errorf("notified %v", h.notified)
	}
}
