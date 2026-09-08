package preview

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
)

func TestChaptersSortValidateAndCache(t *testing.T) {
	calls := 0
	service := New()
	service.run = func(context.Context, *os.File, bool, int64) ([]byte, error) {
		calls++
		return []byte(`{"chapters":[{"start_time":"20","end_time":"40","tags":{"title":"Arrival"}},{"start_time":"0","end_time":"20","tags":{"title":"Opening"}},{"start_time":"NaN","end_time":"30"}]}`), nil
	}
	first, err := service.Chapters(context.Background(), nil, "source")
	if err != nil || len(first) != 2 || first[0].Title != "Opening" || first[1].StartMS != 20000 {
		t.Fatalf("chapters %#v %v", first, err)
	}
	first[0].Title = "mutated"
	second, _ := service.Chapters(context.Background(), nil, "source")
	if calls != 1 || second[0].Title != "Opening" {
		t.Fatal("cache not immutable")
	}
}
func TestCanceledAndBusyRequestsDoNotStartWork(t *testing.T) {
	service := New()
	service.run = func(context.Context, *os.File, bool, int64) ([]byte, error) {
		t.Fatal("unexpected subprocess")
		return nil, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Frame(ctx, nil, "a", 0); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	service.slot <- struct{}{}
	if _, err := service.Frame(context.Background(), nil, "a", 0); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
}
func TestFrameBoundsAndNonImageRejected(t *testing.T) {
	service := New()
	service.run = func(context.Context, *os.File, bool, int64) ([]byte, error) { return []byte("not an image"), nil }
	if _, err := service.Frame(context.Background(), nil, "a", -1); err == nil {
		t.Fatal("negative accepted")
	}
	if _, err := service.Frame(context.Background(), nil, "a", 0); err == nil {
		t.Fatal("nonimage accepted")
	}
}

func TestCacheEvictionAndOutputBudget(t *testing.T) {
	service := New()
	service.run = func(context.Context, *os.File, bool, int64) ([]byte, error) { return []byte(`{"chapters":[]}`), nil }
	for n := 0; n < maxEntries+10; n++ {
		if _, err := service.Chapters(context.Background(), nil, fmt.Sprint(n)); err != nil {
			t.Fatal(err)
		}
	}
	if len(service.cache) != maxEntries {
		t.Fatal("unbounded cache", len(service.cache))
	}
	if _, ok := service.cache["0:chapters"]; ok {
		t.Fatal("oldest entry retained")
	}
	service.run = func(context.Context, *os.File, bool, int64) ([]byte, error) { return make([]byte, maxOutput+1), nil }
	if _, err := service.Chapters(context.Background(), nil, "oversized"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	var output boundedWriter
	if _, err := output.Write(make([]byte, maxOutput+1)); !errors.Is(err, ErrInvalid) || output.Len() != 0 {
		t.Fatal("output exceeded budget")
	}
}
