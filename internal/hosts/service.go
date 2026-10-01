package hosts

// Service is one service a host advertises over DNS-SD.
type Service struct {
	// Type is the service type without the domain, such as
	// "_googlecast._tcp".
	Type string `json:"type"`

	// Instance is the name the host gives the service, such as "Living Room
	// TV". It is empty when that name is no fit to show a person, such as one
	// holding a control character.
	Instance string `json:"instance,omitempty"`

	// Port is the port the service listens on, and zero when no SRV record
	// came with it.
	Port uint16 `json:"port,omitempty"`

	// Label and Model are the name a Google Cast device shows its owner and
	// its model, such as "Google Nest Mini", from its TXT record. Both are
	// empty for any other service type.
	Label string `json:"label,omitempty"`
	Model string `json:"model,omitempty"`
}
