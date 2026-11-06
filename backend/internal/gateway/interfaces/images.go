package interfaces

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"bosapi/backend/internal/platform/errorcode"
)

// imageGateway handles the Images API for providers with a native OpenAI API
// key. OAuth Codex accounts do not expose the Images API.
func (a *app) imageGateway(w http.ResponseWriter, r *http.Request, p provider, path string) {
	if strings.HasPrefix(path, "images/tasks/") && r.Method == http.MethodGet {
		a.getImageTask(w, r, path)
		return
	}
	if path == "images/generations/async" || path == "images/edits/async" {
		a.submitImageTask(w, r, p, strings.TrimSuffix(path, "/async"))
		return
	}
	if path != "images/generations" && path != "images/edits" {
		fail(w, http.StatusNotFound, errorcode.NotFound)
		return
	}
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, errorcode.MethodNotAllowed)
		return
	}
	if p.Type != "api_key" || p.Platform != "openai" {
		fail(w, http.StatusNotFound, errorcode.ProviderCapabilityUnavailable)
		return
	}
	payload, err := io.ReadAll(http.MaxBytesReader(w, r.Body, requestBodyLimit(r)))
	if err != nil {
		fail(w, http.StatusRequestEntityTooLarge, errorcode.RequestTooLarge)
		return
	}
	contentType := r.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		fail(w, http.StatusBadRequest, errorcode.InvalidRequest)
		return
	}
	var model string
	var streaming bool
	switch mediaType {
	case "application/json":
		var fields map[string]json.RawMessage
		if json.Unmarshal(payload, &fields) != nil || fields == nil || json.Unmarshal(fields["model"], &model) != nil {
			fail(w, http.StatusBadRequest, errorcode.InvalidRequest)
			return
		}
		if raw := fields["stream"]; len(raw) > 0 && json.Unmarshal(raw, &streaming) != nil {
			fail(w, http.StatusBadRequest, errorcode.InvalidRequest)
			return
		}
	case "multipart/form-data":
		boundary := params["boundary"]
		if boundary == "" {
			fail(w, http.StatusBadRequest, errorcode.InvalidRequest)
			return
		}
		reader := multipart.NewReader(bytes.NewReader(payload), boundary)
		for {
			part, readErr := reader.NextPart()
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				fail(w, http.StatusBadRequest, errorcode.InvalidRequest)
				return
			}
			if part.FileName() == "" && (part.FormName() == "model" || part.FormName() == "stream") {
				value, readErr := io.ReadAll(io.LimitReader(part, 1024))
				if readErr != nil {
					fail(w, http.StatusBadRequest, errorcode.InvalidRequest)
					return
				}
				if part.FormName() == "model" {
					model = string(value)
				} else {
					streaming = string(value) == "true"
				}
			}
		}
	default:
		fail(w, http.StatusUnsupportedMediaType, errorcode.UnsupportedMediaType)
		return
	}
	if model == "" || len(model) > 100 || strings.ContainsAny(model, "\r\n") || (len(p.Models) > 0 && !containsModel(p.Models, model)) {
		fail(w, http.StatusBadRequest, errorcode.InvalidModel)
		return
	}
	endpoint, err := providerEndpoint(p, "/"+path)
	if err != nil {
		fail(w, http.StatusInternalServerError, errorcode.InternalError)
		return
	}
	request, err := http.NewRequestWithContext(r.Context(), r.Method, endpoint, bytes.NewReader(payload))
	if err != nil {
		fail(w, http.StatusInternalServerError, errorcode.InternalError)
		return
	}
	request.Header.Set("Authorization", "Bearer "+p.APIKey)
	request.Header.Set("Content-Type", contentType)
	if streaming {
		request.Header.Set("Accept", "text/event-stream")
	}
	res, err := a.client.Do(request)
	if err != nil {
		fail(w, http.StatusBadGateway, errorcode.UpstreamError)
		return
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		if res.StatusCode == http.StatusTooManyRequests {
			writeUpstreamLimitError(w, res, time.Now())
			return
		}
		fail(w, res.StatusCode, errorcode.UpstreamError)
		return
	}
	w.Header().Set("Content-Type", res.Header.Get("Content-Type"))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(res.StatusCode)
	observer := newImageUsageObserver(r, streaming)
	flusher, _ := w.(http.Flusher)
	buffer := make([]byte, 32<<10)
	for {
		n, readErr := res.Body.Read(buffer)
		if n > 0 {
			observer.write(buffer[:n])
			if _, err := w.Write(buffer[:n]); err != nil {
				return
			}
			if streaming && flusher != nil {
				flusher.Flush()
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				observer.finish()
			}
			return
		}
	}

}

type imageTaskRecord struct {
	ID        string          `json:"id"`
	TaskID    string          `json:"task_id"`
	Object    string          `json:"object"`
	Status    string          `json:"status"`
	CreatedAt int64           `json:"created_at"`
	ExpiresAt int64           `json:"expires_at"`
	PollURL   string          `json:"poll_url,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     json.RawMessage `json:"error,omitempty"`
	Owner     string          `json:"-"`
}

type storedImageTask struct {
	imageTaskRecord
	OwnerHash string `json:"owner_hash"`
}

func imageTaskDir() string { return os.Getenv("BOS_IMAGE_TASK_DIR") }

func imageTaskFile(id string) string { return filepath.Join(imageTaskDir(), id+".json") }

func writeImageTask(task storedImageTask) error {
	data, err := json.Marshal(task)
	if err != nil {
		return err
	}
	path := imageTaskFile(task.ID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (a *app) submitImageTask(w http.ResponseWriter, r *http.Request, p provider, path string) {
	if r.Method != http.MethodPost {
		fail(w, 405, errorcode.MethodNotAllowed)
		return
	}
	if imageTaskDir() == "" || p.Type != "api_key" || p.Platform != "openai" {
		fail(w, 404, errorcode.NotFound)
		return
	}
	payload, err := io.ReadAll(http.MaxBytesReader(w, r.Body, requestBodyLimit(r)))
	if err != nil || len(payload) == 0 {
		fail(w, 400, errorcode.InvalidRequest)
		return
	}
	contentType := r.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		fail(w, 400, errorcode.InvalidRequest)
		return
	}
	model, streaming := "", false
	if mediaType == "application/json" {
		var fields map[string]json.RawMessage
		if json.Unmarshal(payload, &fields) != nil || json.Unmarshal(fields["model"], &model) != nil {
			fail(w, 400, errorcode.InvalidRequest)
			return
		}
		if raw := fields["stream"]; len(raw) > 0 && json.Unmarshal(raw, &streaming) != nil {
			fail(w, 400, errorcode.InvalidRequest)
			return
		}
	} else if mediaType == "multipart/form-data" {
		reader := multipart.NewReader(bytes.NewReader(payload), params["boundary"])
		for {
			part, e := reader.NextPart()
			if e == io.EOF {
				break
			}
			if e != nil {
				fail(w, 400, errorcode.InvalidRequest)
				return
			}
			if part.FileName() == "" && (part.FormName() == "model" || part.FormName() == "stream") {
				value, e := io.ReadAll(io.LimitReader(part, 1024))
				if e != nil {
					fail(w, 400, errorcode.InvalidRequest)
					return
				}
				if part.FormName() == "model" {
					model = string(value)
				} else {
					streaming = string(value) == "true"
				}
			}
		}
	} else {
		fail(w, 415, errorcode.UnsupportedMediaType)
		return
	}
	if model == "" || len(model) > 100 || streaming || (len(p.Models) > 0 && !containsModel(p.Models, model)) {
		fail(w, 400, errorcode.InvalidRequest)
		return
	}
	if err := os.MkdirAll(imageTaskDir(), 0700); err != nil {
		fail(w, 503, errorcode.ImageTaskUnavailable)
		return
	}
	id := "imgtask_" + random(18)
	pollURL := "/v1/images/tasks/" + id
	if strings.HasPrefix(r.URL.Path, "/images/") {
		pollURL = "/images/tasks/" + id
	}
	now := time.Now()
	task := storedImageTask{imageTaskRecord: imageTaskRecord{ID: id, TaskID: id, Object: "image.task", Status: "processing", CreatedAt: now.Unix(), ExpiresAt: now.Add(24 * time.Hour).Unix(), PollURL: pollURL}, OwnerHash: digest(r.Header.Get("Authorization"))}
	if err := writeImageTask(task); err != nil {
		fail(w, 503, errorcode.ImageTaskUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Location", pollURL)
	w.Header().Set("Retry-After", "3")
	respond(w, 202, task.imageTaskRecord)
	go a.runImageTask(task, p, path, contentType, payload)
}

func (a *app) runImageTask(task storedImageTask, p provider, path, contentType string, payload []byte) {
	timeout := a.client.Timeout
	if timeout <= 0 {
		timeout = 300 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	endpoint, err := providerEndpoint(p, "/"+path)
	if err == nil {
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		err = requestErr
		if err == nil {
			request.Header.Set("Authorization", "Bearer "+p.APIKey)
			request.Header.Set("Content-Type", contentType)
			var res *http.Response
			res, err = a.client.Do(request)
			if err == nil {
				defer res.Body.Close()
				if res.StatusCode < 200 || res.StatusCode >= 300 {
					err = errors.New("upstream rejected image request")
				} else {
					resultPath := filepath.Join(imageTaskDir(), task.ID+".result")
					var file *os.File
					file, err = os.OpenFile(resultPath+".tmp", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
					if err == nil {
						_, err = io.Copy(file, io.LimitReader(res.Body, 64<<20))
						if closeErr := file.Close(); err == nil {
							err = closeErr
						}
						if err == nil {
							err = os.Rename(resultPath+".tmp", resultPath)
						}
						if err != nil {
							_ = os.Remove(resultPath + ".tmp")
						}
					}
				}
			}
		}
	}
	if err != nil {
		task.Status = "failed"
		taskError, _ := json.Marshal(map[string]string{"code": string(errorcode.UpstreamError), "message": "image generation failed"})
		task.Error = json.RawMessage(taskError)
	} else {
		task.Status = "completed"
	}
	_ = writeImageTask(task)
}

func (a *app) getImageTask(w http.ResponseWriter, r *http.Request, path string) {
	if imageTaskDir() == "" {
		fail(w, 404, errorcode.NotFound)
		return
	}
	id := strings.TrimPrefix(path, "images/tasks/")
	if !strings.HasPrefix(id, "imgtask_") || strings.ContainsAny(id, "/.") {
		fail(w, 404, errorcode.NotFound)
		return
	}
	data, err := os.ReadFile(imageTaskFile(id))
	if err != nil {
		fail(w, 404, errorcode.NotFound)
		return
	}
	var task storedImageTask
	if json.Unmarshal(data, &task) != nil || task.OwnerHash != digest(r.Header.Get("Authorization")) || time.Now().Unix() > task.ExpiresAt {
		fail(w, 404, errorcode.NotFound)
		return
	}
	if task.Status == "completed" {
		result, err := os.ReadFile(filepath.Join(imageTaskDir(), id+".result"))
		if err != nil || !json.Valid(result) {
			fail(w, 503, errorcode.ImageTaskUnavailable)
			return
		}
		task.Result = result
	}
	w.Header().Set("Cache-Control", "no-store")
	if task.Status == "processing" {
		w.Header().Set("Retry-After", "3")
	}
	respond(w, 200, task.imageTaskRecord)
}

func containsModel(models []string, model string) bool {
	for _, allowed := range models {
		if allowed == model {
			return true
		}
	}
	return false
}
