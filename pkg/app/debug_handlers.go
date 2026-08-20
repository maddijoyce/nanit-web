package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/indiefan/home_assistant_nanit/pkg/baby"
	"github.com/indiefan/home_assistant_nanit/pkg/client"
	"github.com/rs/zerolog/log"
	"google.golang.org/protobuf/proto"
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
// docs/soundtrack-capture.md is the runbook for using them.
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

// debugPlaybackRequest - a PUT_PLAYBACK, optionally carrying an extra field.
//
// The Soundtrack fields are known and set from Name. Tag/Value/Text add one
// further field on top, which is how a candidate duration is tested: the Nanit
// app offers 30 minute, 60 minute and infinite timers, so Playback almost
// certainly carries a duration this project does not yet send.
type debugPlaybackRequest struct {
	BabyUID string `json:"baby_uid"`
	// Stop - send status STOPPED and nothing else
	Stop bool `json:"stop,omitempty"`
	// Name - the soundtrack filename. Sets the mapped Soundtrack on fields 3
	// and 4, exactly as a normal command does.
	Name string `json:"name,omitempty"`
	// SoundtrackType - the Soundtrack's own field 1, which has been 0 in every
	// capture. Set it to probe what it means.
	SoundtrackType int32 `json:"soundtrack_type,omitempty"`
	// SoundtrackTag - protobuf field number for one extra field *inside* each
	// Soundtrack message, rather than beside it on Playback.
	//
	// Soundtrack has only tags 1 and 2 in the schema, and nothing has probed
	// beyond them. A per-track setting such as the repeat mode would sit here
	// rather than on Playback, which matches the app offering the choice per
	// sound.
	SoundtrackTag int32 `json:"soundtrack_tag,omitempty"`
	// SoundtrackValue - varint payload for SoundtrackTag
	SoundtrackValue uint64 `json:"soundtrack_value,omitempty"`
	// SoundtrackText - length-delimited payload for SoundtrackTag, used when
	// SoundtrackValue is not set
	SoundtrackText string `json:"soundtrack_text,omitempty"`

	// RequestTag - protobuf field number for one extra field on the Request
	// envelope itself, rather than on Playback.
	//
	// Request has tags 3, 9, 10, 11, 14 and 19+ free. Unlikely to carry a
	// playback mode, since its fields are per request type, but it is the last
	// untested surface on the play command.
	RequestTag int32 `json:"request_tag,omitempty"`
	// RequestValue - varint payload for RequestTag
	RequestValue uint64 `json:"request_value,omitempty"`

	// Tag - protobuf field number for one extra field on Playback
	Tag int32 `json:"tag,omitempty"`
	// Value - varint payload for Tag. Used when UseVarint is set.
	//
	// Wire type matters: a tag the camera maps as a varint rejects a
	// length-delimited payload outright, which shows up as a request timeout
	// rather than an error response.
	Value uint64 `json:"value,omitempty"`
	// Text - length-delimited payload for Tag, used when UseVarint is not set
	Text string `json:"text,omitempty"`
	// UseVarint - encode Value as a varint rather than Text as bytes
	UseVarint bool `json:"varint,omitempty"`
}

// handleDebugPlaybackAPI - sends a PUT_PLAYBACK, optionally carrying the track
// name under a candidate field tag
func handleDebugPlaybackAPI(w http.ResponseWriter, r *http.Request, babies []baby.Baby, app *App) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var requestData debugPlaybackRequest
	if err := json.NewDecoder(r.Body).Decode(&requestData); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
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

	playback := &client.Playback{Status: client.Playback_STARTED.Enum()}
	encoding := "none"

	if requestData.Stop {
		playback.Status = client.Playback_STOPPED.Enum()
	} else {
		if requestData.Name != "" {
			playback.Soundtrack = client.NewSoundtrackMessage(requestData.Name)
			playback.SelectedSoundtrack = client.NewSoundtrackMessage(requestData.Name)

			if requestData.SoundtrackType != 0 {
				playback.Soundtrack.Type = proto.Int32(requestData.SoundtrackType)
				playback.SelectedSoundtrack.Type = proto.Int32(requestData.SoundtrackType)
			}

			if requestData.SoundtrackTag > 0 {
				for _, entry := range []*client.Soundtrack{playback.Soundtrack, playback.SelectedSoundtrack} {
					if requestData.SoundtrackText != "" {
						client.SetUnknownBytesField(entry, requestData.SoundtrackTag, []byte(requestData.SoundtrackText))
					} else {
						client.SetUnknownVarintField(entry, requestData.SoundtrackTag, requestData.SoundtrackValue)
					}
				}
			}
		}

		if requestData.Tag > 0 {
			if requestData.UseVarint {
				encoding = "varint"
				client.SetUnknownVarintField(playback, requestData.Tag, requestData.Value)
			} else if requestData.Text != "" {
				encoding = "bytes"
				client.SetUnknownBytesField(playback, requestData.Tag, []byte(requestData.Text))
			}
		}
	}

	log.Warn().
		Str("baby_uid", babyUID).
		Bool("stop", requestData.Stop).
		Int32("tag", requestData.Tag).
		Str("name", requestData.Name).
		Uint64("value", requestData.Value).
		Str("encoding", encoding).
		Str("sent", playback.String()).
		Msg("DEBUG: sending experimental PUT_PLAYBACK")

	req := &client.Request{Playback: playback}
	if requestData.RequestTag > 0 {
		client.SetUnknownVarintField(req, requestData.RequestTag, requestData.RequestValue)
	}

	awaitResponse := conn.SendRequest(client.RequestType_PUT_PLAYBACK, req)

	result := map[string]interface{}{
		"baby_uid": babyUID,
		"stop":     requestData.Stop,
		"tag":      requestData.Tag,
		"name":     requestData.Name,
		"value":    requestData.Value,
		"encoding": encoding,
		"sent":     playback.String(),
	}

	if res, err := awaitResponse(10 * time.Second); err != nil {
		result["response_error"] = err.Error()
	} else {
		result["status_code"] = res.GetStatusCode()
		result["status_message"] = res.GetStatusMessage()
		result["raw"] = res.String()

		log.Warn().
			Int32("status_code", res.GetStatusCode()).
			Str("status_message", res.GetStatusMessage()).
			Msg("DEBUG: experimental PUT_PLAYBACK response")
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
		debugError(w, http.StatusGatewayTimeout, "GET_SOUNDTRACKS failed: %v", err)
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

// debugGetRequest - issues an arbitrary GET request and dumps everything that
// comes back, mapped or not
type debugGetRequest struct {
	BabyUID string `json:"baby_uid"`
	// Type - request type name, e.g. "GET_CONTROL" or "GET_SETTINGS"
	Type string `json:"type"`
	// SelectorTag - attach the selector as an embedded message at this tag on
	// Request, rather than using the generated field.
	//
	// The camera rejects some requests with "missed 'getsettings' field", naming
	// a selector the schema has no mapping for. Request has tags 3, 6, 9, 10,
	// 11, 14 and 19+ free; sweeping them with this finds the right one, because
	// the wrong tag keeps producing that same error and the right one does not.
	SelectorTag int32 `json:"selector_tag,omitempty"`
	// LogsURL - GetLogs.url, required by GET_LOGS
	LogsURL string `json:"logs_url,omitempty"`
	// Flags - selector field tags to set true on the request's selector message.
	//
	// Several GET requests are filtered: GetControl asks per-item (ptz=1,
	// nightLight=2, nightLightTimeout=3, sensorDataTransferEn=4), so the camera
	// only reports what was asked for. Setting unmapped selector tags is how you
	// find out whether the camera will report something the schema has no name
	// for.
	Flags []int32 `json:"flags,omitempty"`
}

// handleDebugGetAPI - sends any GET request type and returns the decoded reply
// together with every field the schema does not map
func handleDebugGetAPI(w http.ResponseWriter, r *http.Request, babies []baby.Baby, app *App) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var requestData debugGetRequest
	if err := json.NewDecoder(r.Body).Decode(&requestData); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	typeValue, ok := client.RequestType_value[requestData.Type]
	if !ok {
		debugError(w, http.StatusBadRequest, "unknown request type %q", requestData.Type)
		return
	}
	requestType := client.RequestType(typeValue)

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

	req := buildDebugGetRequest(requestType, requestData.Flags)

	if requestData.LogsURL != "" {
		req.GetLogs = &client.GetLogs{Url: proto.String(requestData.LogsURL)}
	}

	// An unmapped selector, attached by tag. Flags default to a single "all"
	// style boolean when none are given, since that is the shape every mapped
	// selector uses.
	if requestData.SelectorTag > 0 {
		flags := requestData.Flags
		if len(flags) == 0 {
			flags = []int32{1}
		}

		client.SetUnknownBytesField(req, requestData.SelectorTag, client.EncodeSelectorMessage(flags))
	}

	log.Warn().
		Str("baby_uid", babyUID).
		Str("type", requestData.Type).
		Interface("flags", requestData.Flags).
		Int32("selector_tag", requestData.SelectorTag).
		Str("sent", req.String()).
		Msg("DEBUG: sending experimental GET request")

	awaitResponse := conn.SendRequest(requestType, req)

	res, err := awaitResponse(30 * time.Second)
	if err != nil {
		debugError(w, http.StatusGatewayTimeout, "%v failed: %v", requestData.Type, err)
		return
	}

	result := map[string]interface{}{
		"baby_uid":       babyUID,
		"type":           requestData.Type,
		"status_code":    res.GetStatusCode(),
		"status_message": res.GetStatusMessage(),
		"unknown_fields": client.DescribeUnknownFields(res),
		"raw":            res.String(),
		"sent":           req.String(),
	}

	log.Warn().
		Str("type", requestData.Type).
		Int32("status_code", res.GetStatusCode()).
		Str("raw", res.String()).
		Msg("DEBUG: experimental GET response")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// buildDebugGetRequest - attaches the selector message the request type expects,
// with the requested tags set true.
//
// Selector flags are written as unknown varint fields. On the wire a proto2
// bool is a varint 1, so this is byte-identical to setting the generated field
// while also allowing tags the schema has no name for.
func buildDebugGetRequest(requestType client.RequestType, flags []int32) *client.Request {
	req := &client.Request{}

	var selector proto.Message
	switch requestType {
	case client.RequestType_GET_CONTROL:
		control := &client.GetControl{}
		req.GetControl_ = control
		selector = control
	case client.RequestType_GET_STATUS:
		status := &client.GetStatus{}
		req.GetStatus_ = status
		selector = status
	case client.RequestType_GET_SETTINGS:
		settings := &client.GetSettings{}
		req.GetSettings_ = settings
		selector = settings
	case client.RequestType_GET_SENSOR_DATA:
		sensorData := &client.GetSensorData{}
		req.GetSensorData = sensorData
		selector = sensorData
	default:
		// GET_SETTINGS and friends take no selector
		return req
	}

	if len(flags) == 0 {
		// Every mapped selector uses field 1 as its "all" switch
		flags = []int32{1}
	}

	for _, tag := range flags {
		if tag < 1 {
			continue
		}

		client.SetUnknownVarintField(selector, tag, 1)
	}

	return req
}

// debugRestRequest - fetches a path on Nanit's cloud API
type debugRestRequest struct {
	// Path - path on api.nanit.com, e.g. "/babies" or "/babies/<uid>"
	Path string `json:"path"`
}

// handleDebugRestAPI - GETs an arbitrary Nanit API path and returns the raw body.
//
// The camera websocket is not the only channel the phone app uses. A setting
// the camera merely remembers — such as the selected soundtrack — may well be
// stored server-side, in which case it never appears on the socket at all and
// can only be found by diffing the REST payloads.
func handleDebugRestAPI(w http.ResponseWriter, r *http.Request, app *App) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var requestData debugRestRequest
	if err := json.NewDecoder(r.Body).Decode(&requestData); err != nil {
		debugError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if requestData.Path == "" {
		debugError(w, http.StatusBadRequest, "path is required")
		return
	}

	// Only paths on the Nanit API; this must not become a general fetcher
	if strings.Contains(requestData.Path, "://") {
		debugError(w, http.StatusBadRequest, "path must be a path, not a URL")
		return
	}

	if app.RestClient == nil {
		debugError(w, http.StatusServiceUnavailable, "no Nanit API client")
		return
	}

	log.Warn().Str("path", requestData.Path).Msg("DEBUG: fetching Nanit API path")

	body, err := app.RestClient.FetchPathRaw(requestData.Path)
	if err != nil {
		debugError(w, http.StatusBadGateway, "%v", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	// Pass the payload through untouched when it is JSON, so it can be diffed
	// directly; fall back to a wrapper when it is not.
	if json.Valid(body) {
		w.Write(body)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"path": requestData.Path,
		"raw":  string(body),
	})
}

// debugError - responds with a JSON error, so a piped `jq` still parses the
// output instead of choking on a plain-text error body
func debugError(w http.ResponseWriter, status int, format string, args ...interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error":  fmt.Sprintf(format, args...),
		"status": status,
	})
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
