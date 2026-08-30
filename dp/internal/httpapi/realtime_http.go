package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"DP/internal/domain"
)

func (a *API) realtimeEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		a.writeError(w, r, errors.New("streaming is not supported"))
		return
	}
	if a.realtime == nil {
		a.writeError(w, r, errors.New("realtime events are not configured"))
		return
	}
	subscription := a.realtime.Subscribe(currentUser(r).ID)
	defer subscription.Close()
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		a.writeError(w, r, domain.ErrUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	writeRealtimeSync(w)
	flusher.Flush()

	heartbeatInterval := a.realtimeBeat
	if heartbeatInterval <= 0 {
		heartbeatInterval = 15 * time.Second
	}
	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-subscription.Done:
			return
		case event := <-subscription.Events:
			content, marshalErr := json.Marshal(event)
			if marshalErr != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", event.ID, event.Type, content); err != nil {
				return
			}
			flusher.Flush()
		case <-heartbeat.C:
			if _, _, err := a.auth.AuthenticateSession(r.Context(), cookie.Value); err != nil {
				return
			}
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeRealtimeSync(w http.ResponseWriter) {
	content, _ := json.Marshal(map[string]any{"occurred_at": time.Now().UTC()})
	fmt.Fprintf(w, "event: sync\ndata: %s\n\n", content)
}
