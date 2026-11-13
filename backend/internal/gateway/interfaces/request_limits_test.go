package interfaces

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequestBodyReadFailureStopsBeforeQuota(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"timeout", os.ErrDeadlineExceeded, http.StatusRequestTimeout, "request_timeout"},
		{"incomplete", io.ErrUnexpectedEOF, http.StatusBadRequest, "invalid_request"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, secret := setupToolGateway(t, &toolRecordingSink{}, "", "application/json")
			quota := &organizationQuotaStub{allowed: true}
			a.organizationQuota = quota
			r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			ctx, cancel := context.WithCancel(r.Context())
			defer cancel()
			r = r.WithContext(ctx)
			body := &failedRequestBody{err: tc.err, cancel: cancel}
			r.Body = body
			r.Header.Set("Authorization", "Bearer "+secret)
			w := httptest.NewRecorder()
			a.routes().ServeHTTP(w, r)
			if w.Code != tc.status || !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if len(quota.checked) != 0 || body.reads != 1 {
				t.Fatalf("quota checks=%v body reads=%d", quota.checked, body.reads)
			}
		})
	}
}

type failedRequestBody struct {
	err    error
	cancel context.CancelFunc
	reads  int
}

func (b *failedRequestBody) Read([]byte) (int, error) {
	b.reads++
	b.cancel()
	return 0, b.err
}

func (*failedRequestBody) Close() error { return nil }

func TestRequestBodyLimitBoundaries(t *testing.T) {
	for _, tt := range []struct {
		name   string
		size   int
		status int
	}{
		{"above_previous_limit", (20 << 20) + 1, 200},
		{"at_limit", 128 << 20, 200},
		{"over_limit", (128 << 20) + 1, 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := testApp(t)
			router := gin.New()
			router.Use(a.gatewayBodyLimitMiddleware(a.requestBodyLimit()))
			router.POST("/", func(c *gin.Context) {
				if _, err := io.Copy(io.Discard, c.Request.Body); err != nil {
					c.Status(http.StatusBadRequest)
					return
				}
				c.Status(http.StatusOK)
			})
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", io.LimitReader(repeatedByteReader{}, int64(tt.size))))
			if w.Code != tt.status {
				t.Fatalf("size %d: got %d, want %d", tt.size, w.Code, tt.status)
			}
		})
	}
}

func TestLargeChatRequestReachesUpstream(t *testing.T) {
	a, secret := setupToolGateway(t, &toolRecordingSink{}, `{"choices":[{"message":{"content":"ok"}}]}`, "application/json")
	payload := `{"model":"test","messages":[{"role":"user","content":"` + strings.Repeat("x", 21<<20) + `"}]}`
	called := false
	a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		got, err := io.ReadAll(r.Body)
		if err != nil || len(got) < 21<<20 {
			t.Fatalf("upstream request was truncated: %d, %v", len(got), err)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"}}]}`))}, nil
	})}
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
	r.Header.Set("Authorization", "Bearer "+secret)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != http.StatusOK || !called {
		t.Fatalf("large request: status %d, called %v, body %s", w.Code, called, w.Body.String())
	}
}

// repeatedByteReader exercises large boundaries without allocating a full body.
type repeatedByteReader struct{}

func (repeatedByteReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

func TestConfiguredRequestBodyLimit(t *testing.T) {
	for _, limit := range []int64{1 << 20, 4 << 20} {
		a, secret := setupToolGateway(t, &toolRecordingSink{}, `{"choices":[{"message":{"content":"ok"}}]}`, "application/json")
		a.maxRequestBodyBytes = limit
		payload := `{"model":"test","messages":[{"role":"user","content":"` + strings.Repeat("x", 2<<20) + `"}]}`
		r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
		r.Header.Set("Authorization", "Bearer "+secret)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		a.routes().ServeHTTP(w, r)
		want := http.StatusOK
		if limit < int64(len(payload)) {
			want = http.StatusBadRequest
		}
		if w.Code != want {
			t.Fatalf("limit %d: got %d, want %d", limit, w.Code, want)
		}
	}
}

func TestRequestBodyLimitContext(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	if got := requestBodyLimit(r); got != 128<<20 {
		t.Fatalf("default limit=%d", got)
	}
	if got := requestBodyLimit(withRequestBodyLimit(r, 256<<20)); got != 256<<20 {
		t.Fatalf("configured limit=%d", got)
	}
	if got := requestBodyLimit(withRequestBodyLimit(r, 0)); got != 128<<20 {
		t.Fatalf("fallback limit=%d", got)
	}
}
