package classify

// Label returns the name of c for a reader, such as "Access point". It is
// empty for Unknown and for a class that it does not know. The class values
// are lower-snake identifiers in the storage layer. Only this function spells
// them for a person.
func (c Class) Label() string {
	switch c {
	case Router:
		return "Router"
	case Switch:
		return "Switch"
	case AccessPoint:
		return "Access point"
	case Firewall:
		return "Firewall"
	case Server:
		return "Server"
	case NAS:
		return "NAS"
	case Hypervisor:
		return "Hypervisor"
	case Desktop:
		return "Desktop"
	case Laptop:
		return "Laptop"
	case Phone:
		return "Phone"
	case Tablet:
		return "Tablet"
	case Printer:
		return "Printer"
	case Camera:
		return "Camera"
	case TV:
		return "TV"
	case Streaming:
		return "Media player"
	case Speaker:
		return "Speaker"
	case VoiceAssistant:
		return "Voice assistant"
	case GameConsole:
		return "Game console"
	case IoTHub:
		return "Smart-home hub"
	case SmartHome:
		return "Smart-home device"
	case Wearable:
		return "Wearable"
	case VoIP:
		return "VoIP phone"
	}

	return ""
}
