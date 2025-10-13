package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"sort"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
)

// LokiPatternEntry represents a single pattern block from Loki.
type LokiPatternEntry struct {
	Pattern string    `json:"pattern"`
	Samples [][]int64 `json:"samples"` // [[timestamp, count], ...]
}

// LokiPatternsResponse mirrors Loki's response for /loki/api/v1/patterns.
type LokiPatternsResponse struct {
	Status string             `json:"status,omitempty"`
	Data   []LokiPatternEntry `json:"data"`
}

// HandleLokiPatterns aggregates /patterns responses from multiple Loki instances.
//
// Each response is expected to be in the form:
//
//	{
//	  "status":"success",
//	  "data":[
//	    {"pattern":"...","samples":[[1711839260,1],[1711839270,2]]}
//	  ]
//	}
func HandleLokiPatterns(w http.ResponseWriter, results <-chan *http.Response, logger log.Logger) {
	// merged[pattern][timestamp] = count
	merged := make(map[string]map[int64]int64)

	for resp := range results {
		if resp == nil || resp.Body == nil {
			level.Warn(logger).Log("msg", "nil response or body received for patterns")
			continue
		}
		func() {
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				level.Error(logger).Log("msg", "failed to read patterns response body", "err", err)
				return
			}

			level.Debug(logger).Log("msg", "received body for patterns", "body", string(body))

			var lr LokiPatternsResponse
			if err := json.Unmarshal(body, &lr); err != nil {
				level.Error(logger).Log("msg", "failed to unmarshal patterns response", "err", err)
				return
			}

			for _, entry := range lr.Data {
				if _, ok := merged[entry.Pattern]; !ok {
					merged[entry.Pattern] = make(map[int64]int64)
				}
				for _, pair := range entry.Samples {
					// Defensive parsing: accept [ts,count] of len>=2, ignore bad shapes.
					if len(pair) < 2 {
						continue
					}
					ts := pair[0]
					cnt := pair[1]
					merged[entry.Pattern][ts] += cnt
				}
			}
		}()
	}

	// Rebuild final response: sort timestamps within each pattern; sort patterns.
	out := make([]LokiPatternEntry, 0, len(merged))
	for pattern, tsMap := range merged {
		// Collect and sort timestamps.
		timestamps := make([]int64, 0, len(tsMap))
		for ts := range tsMap {
			timestamps = append(timestamps, ts)
		}
		slices.Sort(timestamps)

		samples := make([][]int64, 0, len(timestamps))
		for _, ts := range timestamps {
			samples = append(samples, []int64{ts, tsMap[ts]})
		}

		out = append(out, LokiPatternEntry{
			Pattern: pattern,
			Samples: samples,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Pattern < out[j].Pattern })

	final := LokiPatternsResponse{
		Status: "success",
		Data:   out,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(final); err != nil {
		level.Error(logger).Log("msg", "failed to encode final patterns response", "err", err)
	}
}
