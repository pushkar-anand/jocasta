package routeros

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Registration is one Wi-Fi client on a radio of the router. The client reads
// it from the Wi-Fi package that the router runs.
type Registration struct {
	// Interface is the radio, or the virtual access point on it, that the
	// client joined.
	Interface  string `json:"interface"`
	MACAddress string `json:"mac-address"`

	// SSID is the name of the network that the client joined. The wifi
	// package reports it for each client. For the wireless package, it comes
	// from the interface.
	SSID string `json:"ssid"`

	// Band is the band, as the router renders it, such as "5ghz-ax" or
	// "2ghz-n".
	Band string `json:"band"`

	// Signal is the signal of the client at the radio in dBm, as the router
	// renders it: "-58" in the wifi package, "-61@6Mbps" in the wireless
	// package. [Registration.DBM] reads both.
	Signal string `json:"signal"`
	Uptime string `json:"uptime"`

	// TxRate is the last rate at which the radio sent to the client. RxRate
	// is the last rate at which it received from the client.
	TxRate Rate `json:"tx-rate"`
	RxRate Rate `json:"rx-rate"`
}

// DBM returns the signal of the client in dBm. It returns zero when the router
// reported no signal.
func (g Registration) DBM() int {
	head, _, _ := strings.Cut(g.Signal, "@")

	dbm, err := strconv.Atoi(strings.TrimSpace(head))
	if err != nil {
		return 0
	}

	return dbm
}

// wirelessRegistration is a row of the registration table of the wireless
// package. This table does not have the SSID or the band.
type wirelessRegistration struct {
	Interface      string `json:"interface"`
	MACAddress     string `json:"mac-address"`
	SignalStrength string `json:"signal-strength"`
	Uptime         string `json:"uptime"`
	TxRate         Rate   `json:"tx-rate"`
	RxRate         Rate   `json:"rx-rate"`
}

// wirelessInterface is a row of /interface/wireless. The client reads it for
// what the registration table does not have.
type wirelessInterface struct {
	Name string `json:"name"`
	SSID string `json:"ssid"`
	Band string `json:"band"`
}

// Registrations returns the Wi-Fi clients of the router. They come from the
// wifi package on RouterOS 7.13 and later, or from the wireless package before.
// A router with neither has no Wi-Fi, so the answer is empty.
func (r *RouterOS) Registrations(ctx context.Context) ([]Registration, error) {
	regs, err := list[Registration](ctx, r, wifiRegistrationAPI)
	if err == nil {
		return regs, nil
	}

	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	return r.wirelessRegistrations(ctx)
}

// wirelessRegistrations reads the wireless package. It joins each client to its
// interface for the SSID and band.
func (r *RouterOS) wirelessRegistrations(ctx context.Context) ([]Registration, error) {
	rows, err := list[wirelessRegistration](ctx, r, wirelessRegistrationAPI)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	ifaces, err := list[wirelessInterface](ctx, r, wirelessAPI)
	if err != nil {
		return nil, fmt.Errorf("read wireless interfaces: %w", err)
	}

	byName := make(map[string]wirelessInterface, len(ifaces))
	for _, i := range ifaces {
		byName[i.Name] = i
	}

	out := make([]Registration, 0, len(rows))

	for _, row := range rows {
		i := byName[row.Interface]

		out = append(out, Registration{
			Interface:  row.Interface,
			MACAddress: row.MACAddress,
			SSID:       i.SSID,
			Band:       i.Band,
			Signal:     row.SignalStrength,
			Uptime:     row.Uptime,
			TxRate:     row.TxRate,
			RxRate:     row.RxRate,
		})
	}

	return out, nil
}
