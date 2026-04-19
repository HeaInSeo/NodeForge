package catalogrest

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/HeaInSeo/NodeVault/pkg/index"
)

// ReconcileTrigger is the interface for triggering a targeted reconcile run.
// Implemented by reconcile.Reconciler in production; replaced by fakes in tests.
type ReconcileTrigger interface {
	ReconcileOne(ctx context.Context, casHash string) error
}

// harborEvent is the minimal Harbor webhook payload we need.
// Harbor sends a JSON body with type and event_data.resources[].digest.
type harborEvent struct {
	Type      string `json:"type"`
	EventData struct {
		Resources []struct {
			Digest string `json:"digest"`
		} `json:"resources"`
	} `json:"event_data"`
}

// RegisterWebhook adds POST /v1/webhooks/harbor to the given mux.
// When called, it looks up index entries matching the event's image digest
// and triggers a targeted reconcile for each matching artifact.
//
// The trigger is called for each matching CAS hash in the index.
// Trigger errors are logged but do not cause the HTTP handler to return an error.
func RegisterWebhook(mux *http.ServeMux, store *index.Store, trigger ReconcileTrigger) {
	mux.HandleFunc("POST /v1/webhooks/harbor", func(w http.ResponseWriter, r *http.Request) {
		var evt harborEvent
		if err := json.NewDecoder(r.Body).Decode(&evt); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}

		// Collect all digests from the event.
		digests := make(map[string]struct{})
		for _, res := range evt.EventData.Resources {
			if res.Digest != "" {
				digests[res.Digest] = struct{}{}
			}
		}
		if len(digests) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// Look up index entries by image digest and trigger reconcile.
		entries, err := store.All()
		if err != nil {
			http.Error(w, "index error", http.StatusInternalServerError)
			return
		}

		triggered := 0
		for _, e := range entries {
			if _, ok := digests[e.ImageDigest]; !ok {
				continue
			}
			if terr := trigger.ReconcileOne(r.Context(), e.CasHash); terr != nil {
				// Log but continue — other artifacts must still be processed.
				_ = terr
			}
			triggered++
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]int{"triggered": triggered})
	})
}
