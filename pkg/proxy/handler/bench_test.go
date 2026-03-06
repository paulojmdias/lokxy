package handler

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/go-kit/log"

	"github.com/paulojmdias/lokxy/pkg/proxy/proxyresponse"
)

// Realistic payloads representative of production Loki responses.
var (
	benchLabelsBody = `{"status":"success","data":["app","cluster","environment","instance","job","namespace","pod","region","service","team"]}`

	benchStreamsBody = `{"status":"success","data":{"resultType":"streams","result":[` +
		`{"stream":{"app":"nginx","env":"prod","region":"us-east-1"},"values":[` +
		`["1700000000000000000","GET /api/v1/users 200 12ms"],` +
		`["1700000001000000000","POST /api/v1/orders 201 45ms"]` +
		`]},` +
		`{"stream":{"app":"api","env":"prod","region":"us-east-1"},"values":[` +
		`["1700000002000000000","INFO starting handler"],` +
		`["1700000003000000000","ERROR db timeout"]` +
		`]}` +
		`],"stats":{"summary":{"bytesProcessedPerSecond":102400,"linesProcessedPerSecond":1000,"totalBytesProcessed":512000,"totalLinesProcessed":5000,"execTime":0.05}}}}`

	benchSeriesBody = `{"status":"success","data":[` +
		`{"__name__":"logs","app":"nginx","environment":"prod","region":"us-east-1","instance":"i-aabbccdd"},` +
		`{"__name__":"logs","app":"api","environment":"prod","region":"us-east-1","instance":"i-11223344"},` +
		`{"__name__":"logs","app":"worker","environment":"staging","region":"us-west-2","instance":"i-55667788"}` +
		`]}`
)

// makeResults builds a closed, buffered channel of n BackendResponse items, each
// carrying the given JSON body. Callers must consume all items (handlers do this
// automatically via range).
func makeResults(n int, body string) <-chan *proxyresponse.BackendResponse {
	ch := make(chan *proxyresponse.BackendResponse, n)
	for range n {
		rec := httptest.NewRecorder()
		rec.WriteString(body)
		ch <- &proxyresponse.BackendResponse{
			Response:    rec.Result(),
			BackendName: "bench-backend",
			BackendURL:  "http://bench:3100",
		}
	}
	close(ch)
	return ch
}

func BenchmarkHandleLokiLabels(b *testing.B) {
	logger := log.NewNopLogger()
	for _, tc := range []struct {
		name string
		n    int
	}{
		{"1backend", 1},
		{"2backends", 2},
		{"5backends", 5},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				b.StopTimer()
				results := makeResults(tc.n, benchLabelsBody)
				w := httptest.NewRecorder()
				b.StartTimer()

				HandleLokiLabels(context.Background(), w, results, logger)
			}
		})
	}
}

func BenchmarkHandleLokiQueries_Streams(b *testing.B) {
	logger := log.NewNopLogger()
	for _, tc := range []struct {
		name string
		n    int
	}{
		{"1backend", 1},
		{"2backends", 2},
		{"5backends", 5},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				b.StopTimer()
				results := makeResults(tc.n, benchStreamsBody)
				w := httptest.NewRecorder()
				b.StartTimer()

				HandleLokiQueries(context.Background(), w, results, logger)
			}
		})
	}
}

func BenchmarkHandleLokiSeries(b *testing.B) {
	logger := log.NewNopLogger()
	for _, tc := range []struct {
		name string
		n    int
	}{
		{"1backend", 1},
		{"2backends", 2},
		{"5backends", 5},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				b.StopTimer()
				results := makeResults(tc.n, benchSeriesBody)
				w := httptest.NewRecorder()
				b.StartTimer()

				HandleLokiSeries(context.Background(), w, results, logger)
			}
		})
	}
}
