package retry

import (
	"net/http"
	"testing"
	"time"

	"example.com/retrysim/internal/faketransport"
)

func get(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://example.invalid/items", nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func recorder() (func(time.Duration), *[]time.Duration) {
	var got []time.Duration
	return func(d time.Duration) { got = append(got, d) }, &got
}

func TestSucceedsFirstTry(t *testing.T) {
	tr := faketransport.New(faketransport.Step{Status: 200})
	sleep, waits := recorder()
	res := Do(tr, get(t), DefaultPolicy(), sleep)
	if res.Attempts != 1 || res.Response.StatusCode != 200 || len(*waits) != 0 {
		t.Fatalf("attempts=%d status=%d waits=%v", res.Attempts, res.Response.StatusCode, *waits)
	}
}

func TestRetriesTransientStatus(t *testing.T) {
	tr := faketransport.New(faketransport.Step{Status: 503}, faketransport.Step{Status: 200})
	sleep, waits := recorder()
	res := Do(tr, get(t), DefaultPolicy(), sleep)
	if res.Attempts != 2 || res.Response.StatusCode != 200 {
		t.Fatalf("attempts=%d status=%d", res.Attempts, res.Response.StatusCode)
	}
	if len(*waits) != 1 || (*waits)[0] != 100*time.Millisecond {
		t.Fatalf("waits = %v, want [100ms]", *waits)
	}
}

func TestBackoffDoubles(t *testing.T) {
	tr := faketransport.New(faketransport.Step{Status: 502})
	sleep, waits := recorder()
	res := Do(tr, get(t), DefaultPolicy(), sleep)
	want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond}
	if res.Attempts != 4 || len(*waits) != 3 {
		t.Fatalf("attempts=%d waits=%v", res.Attempts, *waits)
	}
	for i, w := range want {
		if (*waits)[i] != w {
			t.Fatalf("waits = %v, want %v", *waits, want)
		}
	}
	if res.Waited != 700*time.Millisecond {
		t.Fatalf("waited = %v, want 700ms", res.Waited)
	}
}

func TestDoesNotRetryClientErrors(t *testing.T) {
	tr := faketransport.New(faketransport.Step{Status: 404}, faketransport.Step{Status: 200})
	sleep, _ := recorder()
	res := Do(tr, get(t), DefaultPolicy(), sleep)
	if res.Attempts != 1 || res.Response.StatusCode != 404 {
		t.Fatalf("attempts=%d status=%d", res.Attempts, res.Response.StatusCode)
	}
}

func TestRetriesTransportError(t *testing.T) {
	tr := faketransport.New(faketransport.Step{Err: faketransport.ErrConnection}, faketransport.Step{Status: 200})
	sleep, _ := recorder()
	res := Do(tr, get(t), DefaultPolicy(), sleep)
	if res.Attempts != 2 || res.Err != nil {
		t.Fatalf("attempts=%d err=%v", res.Attempts, res.Err)
	}
}
