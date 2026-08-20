package mqtt

// Opts - holds configuration needed to establish connection to the broker
type Opts struct {
	BrokerURL string
	ClientID  string

	Username string
	Password string

	TopicPrefix string

	// DiscoveryEnabled - publish Home Assistant MQTT discovery documents so
	// entities are created automatically
	DiscoveryEnabled bool

	// DiscoveryPrefix - topic prefix Home Assistant listens on for discovery,
	// "homeassistant" unless it has been changed in the HA MQTT integration
	DiscoveryPrefix string
}
