package interfaces

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestGeminiQueryKeyAndCompatibilityPrefixAcrossGateway(t *testing.T) {
	for _, prefix := range []string{"/v1beta", "/v1/v1beta", "/v1"} {
		for _, operation := range []string{"list", "get", "generateContent", "streamGenerateContent"} {
			if prefix == "/v1" && (operation == "list" || operation == "get") {
				continue
			}
			t.Run(prefix+"/"+operation, func(t *testing.T) {
				a, key := geminiTestApp(t)
				var logs bytes.Buffer
				previous := log.Writer()
				log.SetOutput(&logs)
				defer log.SetOutput(previous)
				called := false
				a.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					called = true
					if r.Header.Get("x-goog-api-key") != "provider-key" || r.Header.Get("Authorization") != "" || strings.Contains(r.URL.String(), key) || r.URL.Query().Get("key") != "" {
						t.Fatalf("client credentials reached upstream: %s", r.URL.Path)
					}
					response := `{"name":"models/actual","supportedGenerationMethods":["generateContent","countTokens"]}`
					kind := "application/json"
					expected := "/v1beta/models/actual"
					if operation == "generateContent" || operation == "streamGenerateContent" {
						expected += ":" + operation
						response = `{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}}`
						if operation == "streamGenerateContent" {
							response = "data: " + response + "\n\n"
							kind = "text/event-stream"
							if r.URL.Query().Get("alt") != "sse" {
								t.Fatal("SSE option missing")
							}
						}
					}
					if r.URL.Path != expected {
						t.Fatalf("path=%s expected=%s", r.URL.Path, expected)
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{kind}}, Body: io.NopCloser(strings.NewReader(response))}, nil
				})}
				path := prefix + "/models/alias"
				method := "GET"
				var body io.Reader
				if operation == "list" {
					path = prefix + "/models?pageSize=1&key=" + url.QueryEscape(key)
				} else {
					if operation != "get" {
						path += ":" + operation
						method = "POST"
						body = strings.NewReader(`{"model":"spoof","contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
					}
					path += "?key=" + url.QueryEscape(key)
				}
				request := httptest.NewRequest(method, path, body)
				response := httptest.NewRecorder()
				a.routes().ServeHTTP(response, request)
				if response.Code != 200 || !called {
					t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
				}
				if strings.Contains(logs.String(), key) {
					t.Fatal("query credential leaked into logs")
				}
				if operation == "generateContent" || operation == "streamGenerateContent" {
					event := <-a.usageQueue
					if event.Model != "alias" || event.Metrics.TotalTokens != 15 {
						t.Fatalf("URL model or billing lost: %+v", event)
					}
				}
			})
		}
	}
}

func TestQueryCredentialScopeAndHeaderPrecedence(t *testing.T) {
	for _, test := range []struct{ path, header, value, want, source string }{
		{"/v1beta/models?key=query-secret", "", "", "query-secret", "query-key"},
		{"/v1/v1beta/models?key=query-secret", "", "", "query-secret", "query-key"},
		{"/v1beta/models?key=query-secret", "Authorization", "Bearer header-secret", "header-secret", "authorization"},
		{"/v1beta/models?key=query-secret", "x-goog-api-key", "header-secret", "header-secret", "x-goog-api-key"},
		{"/v1/models?key=query-secret", "", "", "", "none"},
		{"/v1/chat/completions?key=query-secret", "", "", "", "none"},
		{"/v1beta/models?key=first&key=second", "", "", "", "none"},
		{"/v1beta/models?key=query-secret&bad=%ZZ", "", "", "", "none"},
	} {
		r := httptest.NewRequest("GET", test.path, nil)
		if test.header != "" {
			r.Header.Set(test.header, test.value)
		}
		secret, source := requestCredential(r)
		if secret != test.want || source != test.source {
			t.Fatalf("%s source=%s", test.path, source)
		}
	}
}
