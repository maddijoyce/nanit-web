package client

// Soundtrack protocol facts, kept together because both the app and the MQTT
// layer need them.
//
// What is confirmed against a live Nanit Pro:
//   - Playback is driven by PUT_PLAYBACK carrying a Playback message.
//     Playback.status STARTED/STOPPED both work.
//   - GET_SOUNDTRACKS lists the built-in sounds on Response.soundtracks
//     (field 12); the filename is the identifier.
//   - PUT_PLAYBACK with status STARTED plays the track the camera already has
//     selected. It does not select one.
//
// What is not:
//   - How to choose which track plays. Sending the filename as Playback field 2
//     was tried against a camera and ignored: playback started, but on the
//     previously selected track. So selection is either a different field or,
//     more likely, a different request altogether.
//
// See SOUNDTRACK_CAPTURE.md.
const (
	// SoundtrackNameFieldTag - candidate field on Playback for the track name.
	//
	// Tag 2 has been tested and did NOT select the track. It is kept as the
	// default only so the send path has something to carry; change it when a
	// working tag is found.
	SoundtrackNameFieldTag int32 = 2

	// SoundtrackSelectionVerified - whether choosing a specific track is known
	// to work.
	//
	// While false the Home Assistant select entity is not published at all.
	// Publishing it would offer a control that silently plays a different sound
	// than the one chosen, which on a baby monitor is worse than offering
	// nothing. Start/stop is unaffected and is exposed as a switch.
	SoundtrackSelectionVerified = false
)
