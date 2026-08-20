package client

import "google.golang.org/protobuf/proto"

// Soundtrack protocol, confirmed against a live Nanit Pro.
//
//   - Playback is driven by PUT_PLAYBACK carrying a Playback message.
//     Playback.status is STARTED / STOPPED.
//   - GET_SOUNDTRACKS lists the built-in sounds on Response.soundtracks
//     (field 12). The filename is the identifier; there are no numeric ids.
//   - GET_PLAYBACK answers on Response.playback (field 11). While a track is
//     playing it carries the Soundtrack on both field 3 and field 4; when
//     stopped it carries only the status.
//   - PUT_PLAYBACK with status STARTED alone plays whatever the camera already
//     had selected. The track is chosen by including the Soundtrack message.
//
// See docs/soundtrack-capture.md for how this was established.

// SoundtrackSelectionVerified - whether choosing a specific track is known to
// work. Gates the Home Assistant select entity.
//
// State is reconciled against GET_PLAYBACK shortly after every command, so what
// is published is what the camera reports rather than what was asked for.
const SoundtrackSelectionVerified = true

// NewSoundtrackMessage - builds the Soundtrack a Playback command carries.
//
// Type is 0 on every built-in sound the camera reports; its meaning is unknown,
// and it is echoed back rather than invented.
func NewSoundtrackMessage(name string) *Soundtrack {
	return &Soundtrack{
		Type: proto.Int32(0),
		Name: proto.String(name),
	}
}

// PlaybackSoundtrackName - the track a Playback message refers to, preferring
// field 3 and falling back to field 4. Empty when neither is present, which is
// what the camera sends when playback is stopped.
func PlaybackSoundtrackName(playback *Playback) string {
	if playback == nil {
		return ""
	}

	if entry := playback.GetSoundtrack(); entry.GetName() != "" {
		return entry.GetName()
	}

	return playback.GetSelectedSoundtrack().GetName()
}
