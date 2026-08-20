package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/indiefan/home_assistant_nanit/pkg/baby"
	"github.com/indiefan/home_assistant_nanit/pkg/client"
	"github.com/rs/zerolog/log"
)

// ---------------------------------------------------------------------------
// Protocol experiment harness
//
// These endpoints exist to identify control fields that are missing from the
// reverse-engineered schema — principally the soundtrack selector. They put
// operator-supplied bytes on the wire to a baby monitor, so they are refused
// unless NANIT_DEBUG_CONTROL=true is set explicitly. They are never routed in a
// normal run: see registerDebugRoutes.
//
// SOUNDTRACK_CAPTURE.md is the runbook for using them.
// ---------------------------------------------------------------------------

// debugControlRequest - an arbitrary field to place on a PUT_CONTROL message
type debugControlRequest struct {
	BabyUID string `json:"baby_uid"`
	// Tag - the protobuf field number to set on Control
	Tag int32 `json:"tag"`
	// Value - varint payload (ignored when Text is set)
	Value uint64 `json:"value"`
	// Text - length-delimited payload, for candidate fields that are not varints
	Text string `json:"text,omitempty"`
}

// handleDebugControlAPI - sends a single PUT_CONTROL carrying one arbitrary field
func handleDebugControlAPI(w http.ResponseWriter, r *http.Request, babies []baby.Baby, app *App) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var requestData debugControlRequest
	if err := json.NewDecoder(r.Body).Decode(&requestData); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if requestData.Tag < 1 {
		http.Error(w, "tag must be a positive protobuf field number", http.StatusBadRequest)
		return
	}

	babyUID, err := resolveDebugBabyUID(requestData.BabyUID, babies)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	conn := app.getConnection(babyUID)
	if conn == nil {
		http.Error(w, "WebSocket not connected", http.StatusServiceUnavailable)
		return
	}

	control := &client.Control{}
	encoding := "varint"
	if requestData.Text != "" {
		encoding = "bytes"
		client.SetUnknownBytesField(control, requestData.Tag, []byte(requestData.Text))
	} else {
		client.SetUnknownVarintField(control, requestData.Tag, requestData.Value)
	}

	log.Warn().
		Str("baby_uid", babyUID).
		Int32("tag", requestData.Tag).
		Uint64("value", requestData.Value).
		Str("text", requestData.Text).
		Str("encoding", encoding).
		Msg("DEBUG: sending experimental PUT_CONTROL field")

	awaitResponse := conn.SendRequest(client.RequestType_PUT_CONTROL, &client.Request{
		Control: control,
	})

	result := map[string]interface{}{
		"baby_uid": babyUID,
		"tag":      requestData.Tag,
		"encoding": encoding,
		"sent":     true,
	}

	// Report the camera's verdict: a rejected tag usually answers with a
	// non-200 status, which is itself a useful signal while sweeping.
	if res, err := awaitResponse(10 * time.Second); err != nil {
		result["response_error"] = err.Error()
	} else {
		result["status_code"] = res.GetStatusCode()
		result["status_message"] = res.GetStatusMessage()
		result["unknown_fields"] = client.DescribeUnknownFields(res)
		result["raw"] = res.String()

		log.Warn().
			Int32("status_code", res.GetStatusCode()).
			Str("status_message", res.GetStatusMessage()).
			Str("raw", res.String()).
			Msg("DEBUG: experimental PUT_CONTROL response")
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// handleDebugSoundtracksAPI - re-issues GET_SOUNDTRACKS on demand and returns
// the decoded reply, including every field the schema does not map
func handleDebugSoundtracksAPI(w http.ResponseWriter, r *http.Request, babies []baby.Baby, stateManager *baby.StateManager, app *App) {
	if r.Method != "POST" && r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	babyUID, err := resolveDebugBabyUID(r.URL.Query().Get("baby_uid"), babies)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	conn := app.getConnection(babyUID)
	if conn == nil {
		http.Error(w, "WebSocket not connected", http.StatusServiceUnavailable)
		return
	}

	awaitResponse := conn.SendRequest(client.RequestType_GET_SOUNDTRACKS, &client.Request{})

	res, err := awaitResponse(30 * time.Second)
	if err != nil {
		http.Error(w, fmt.Sprintf("GET_SOUNDTRACKS failed: %v", err), http.StatusGatewayTimeout)
		return
	}

	processSoundtracksResponse(babyUID, res, stateManager)

	result := map[string]interface{}{
		"baby_uid":       babyUID,
		"status_code":    res.GetStatusCode(),
		"status_message": res.GetStatusMessage(),
		"unknown_fields": client.DescribeUnknownFields(res),
		"raw":            res.String(),
		"catalog":        stateManager.GetBabyState(babyUID).GetAvailableSoundtracks(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// resolveDebugBabyUID - validates an explicit UID, or picks the only baby when
// one is omitted (the common single-camera case)
func resolveDebugBabyUID(babyUID string, babies []baby.Baby) (string, error) {
	if babyUID == "" {
		if len(babies) != 1 {
			return "", fmt.Errorf("baby_uid is required when %d babies are configured", len(babies))
		}

		return babies[0].UID, nil
	}

	for _, b := range babies {
		if b.UID == babyUID {
			return babyUID, nil
		}
	}

	return "", fmt.Errorf("unknown baby_uid")
}
