package web

// These template names are rendered from outside this package: internal/server
// maps HTTP status codes to them through the error pipeline.
const (
	TemplateLogin = "page/login"
	TemplateSetup = "page/setup"

	TemplateBadRequest = "page/badrequest"
	TemplateNotFound   = "page/notfound"
	TemplateForbidden  = "page/forbidden"
)

const (
	templatePageDashboard = "page/dashboard"
	templatePageDevices   = "page/devices"
	templatePageDevice    = "page/device"
	templatePageNetwork   = "page/network"
	templatePageEvents    = "page/events"
	templatePageScans     = "page/scans"
	templatePageTokens    = "page/tokens"
	templatePageUsers     = "page/users"

	templatePartialTokenList     = "partial/token-list"
	templatePartialLiveOverview  = "partial/live-body"
	templatePartialDeviceRows    = "partial/device-rows"
	templatePartialDeviceRow     = "partial/device-row"
	templatePartialDeviceRowForm = "partial/device-row-form"
	templatePartialDevicePanel   = "partial/device-panel"
)
