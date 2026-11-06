package interfaces

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestResponsesWebSocketLive(t *testing.T) {
	key := os.Getenv("BOS_GATEWAY_KEY")
	if key == "" {
		t.Skip("set BOS_GATEWAY_KEY for the live WebSocket test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+key)
	conn, response, err := websocket.Dial(ctx, "ws://127.0.0.1:8080/v1/responses", &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		if response != nil {
			t.Fatalf("handshake status = %d: %v", response.StatusCode, err)
		}
		t.Fatal(err)
	}
	defer conn.CloseNow()
	payload := []byte(`{"type":"response.create","model":"gpt-5.6-sol","instructions":"","input":[{"role":"user","content":[{"type":"input_text","text":"Reply OK"}]}],"store":false}`)
	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatal(err)
	}
	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var event struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &event) != nil {
			continue
		}
		if event.Type == "response.completed" {
			return
		}
		if event.Type == "response.failed" {
			t.Fatal("upstream response failed")
		}
	}
}
