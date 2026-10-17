package hosts

// Service is one service a host advertises over DNS-SD.
type Service struct {
	// Type is the service type without the domain, such as
	// "_googlecast._tcp".
	Type string `json:"type"`

	// Instance is the name that the host gives the service, such as "Living
	// Room TV". It is empty when that name is not safe to show a person, such
	// as a name with a control character.
	Instance string `json:"instance,omitempty"`

	// Port is the port on which the service listens. It is zero when no SRV
	// record came with it.
	Port uint16 `json:"port,omitempty"`

	// Label is the name that a Google Cast device shows its owner. Model is
	// its model, such as "Google Nest Mini". Both come from its TXT record.
	// Both are empty for any other service type.
	Label string `json:"label,omitempty"`
	Model string `json:"model,omitempty"`
}
