package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"DP/internal/domain"
)

type serviceLogEvent struct {
	Seq     int64     `json:"seq"`
	Time    time.Time `json:"time"`
	Stream  string    `json:"stream"`
	Message string    `json:"message"`
}

func (a *API) streamServiceLogs(w http.ResponseWriter, r *http.Request) {
	if _, err := a.authorizeEnvironment(r, r.PathValue("id")); err != nil {
		a.writeError(w, r, err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		a.writeError(w, r, errors.New("streaming is not supported"))
		return
	}
	tail := 300
	if raw := r.URL.Query().Get("tail"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 || parsed > 2_000 {
			a.writeError(w, r, domain.FieldError("tail", "tail 必须是 0 到 2000 之间的整数"))
			return
		}
		tail = parsed
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	events := make(chan serviceLogEvent, 512)
	done := make(chan error, 1)
	go func() {
		done <- a.serviceLogs.Stream(ctx, r.PathValue("id"), tail, func(stream, message string) {
			event := serviceLogEvent{Time: time.Now().UTC(), Stream: stream, Message: message}
			select {
			case events <- event:
			case <-ctx.Done():
			default:
				cancel()
			}
		})
	}()

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	var seq int64
	writeLog := func(event serviceLogEvent) {
		seq++
		event.Seq = seq
		writeSSE(w, "log", event)
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-events:
			writeLog(event)
			flusher.Flush()
		case err := <-done:
			for {
				select {
				case event := <-events:
					writeLog(event)
				default:
					goto drained
				}
			}
		drained:
			if err != nil && !errors.Is(err, context.Canceled) {
				writeSSE(w, "stream-error", map[string]string{"message": serviceLogError(err)})
			} else {
				writeSSE(w, "end", map[string]string{"message": "日志流已结束"})
			}
			flusher.Flush()
			return
		case <-heartbeat.C:
			fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

func writeSSE(w io.Writer, event string, value any) {
	content, _ := json.Marshal(value)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, content)
}

func serviceLogError(err error) string {
	if errors.Is(err, domain.ErrNotInstalled) {
		return "该服务尚未安装"
	}
	return "无法读取 Docker Compose 日志：" + err.Error()
}
