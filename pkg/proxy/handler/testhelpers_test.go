package handler

import (
	"net/http"

	"github.com/paulojmdias/lokxy/pkg/proxy/proxyresponse"
)

// Helper to wrap http.Response in BackendResponse for tests
func wrapResponse(resp *http.Response) *proxyresponse.BackendResponse {
	return &proxyresponse.BackendResponse{
		Response:    resp,
		BackendName: "test-backend",
		BackendURL:  "http://test-backend:3100",
	}
}
