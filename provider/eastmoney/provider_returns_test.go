package eastmoney

import (
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestGetOneUsesRollingReturn20dWhenF165Present(t *testing.T) {
	const symbol = "600503"
	p := &Provider{
		states: make(map[string]*symState),
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body := `{"rc":0,"data":{"f43":130,"f44":131,"f45":129,"f47":1000,"f60":120,"f170":1.23,"f17":129.9,"f19":130.1,"f165":999}}`
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		})},
	}

	points := make([]DailyPoint, 0, histLen)
	for i := 0; i < histLen; i++ {
		points = append(points, DailyPoint{Close: 100 + float64(i), Volume: 1000})
	}
	p.PreWarm(symbol, points)

	q, err := p.getOne(symbol)
	if err != nil {
		t.Fatalf("getOne returned error: %v", err)
	}

	want := (130.0 - 101.0) / 101.0 * 100.0
	if math.Abs(q.Return20d-want) > 1e-9 {
		t.Fatalf("Return20d = %v, want rolling-history value %v", q.Return20d, want)
	}
	if math.Abs(q.Return20d-999) <= 1e-9 {
		t.Fatalf("Return20d was overwritten by f165; got %v", q.Return20d)
	}
}
