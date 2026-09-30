package routeros

import (
	"context"
	"errors"
	"fmt"
)

// Registration is one Wi-Fi client associated with one of the router's
// radios, read from whichever Wi-Fi package the router runs.
type Registration struct {
	// Interface is the radio, or the virtual access point on it, the client
	// joined.
	Interface  string `json:"interface"`
	MACAddress string `json:"mac-address"`

	// SSID is the network name the client joined. The wifi package reports it
	// per client; on the wireless package it is read off the interface.
	SSID string `json:"ssid"`

	// Band is the router's rendering of the band, such as "5ghz-ax" or
	// "2ghz-n".
	Band string `json:"band"`

	Signal string `json:"signal"`
	Uptime string `json:"uptime"`
}

// wirelessRegistration is a row of the wireless package's registration table,
// which carries neither the SSID nor the band.
type wirelessRegistration struct {
	Interface      string `json:"interface"`
	MACAddress     string `json:"mac-address"`
	SignalStrength string `json:"signal-strength"`
	Uptime         string `json:"uptime"`
}

// wirelessInterface is a row of /interface/wireless, read for what the
// registration table leaves out.
type wirelessInterface struct {
	Name string `json:"name"`
	SSID string `json:"ssid"`
	Band string `json:"band"`
}

// Registrations returns the Wi-Fi clients associated with the router, from the
// wifi package on RouterOS 7.13 and later, or the wireless package before it.
// A router with neither has no Wi-Fi, which is an empty answer.
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

// wirelessRegistrations reads the wireless package, joining each client to its
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
		})
	}

	return out, nil
}
