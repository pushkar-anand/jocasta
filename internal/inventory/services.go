package inventory

import (
	"context"
	"fmt"

	"github.com/pushkar-anand/jocasta/internal/db/models"
	"github.com/pushkar-anand/jocasta/internal/hosts"
)

// recordServices files each of services as advertised by device deviceID at
// p's time. A service the device did not advertise this time keeps its row
// until the prune deletes it, since one sweep can miss an mDNS answer.
func recordServices(ctx context.Context, p *pass, deviceID int64, services []hosts.Service) error {
	for _, sv := range services {
		err := p.q.UpsertDeviceService(ctx, models.UpsertDeviceServiceParams{
			DeviceID: deviceID,
			Type:     sv.Type,
			Instance: sv.Instance,
			Port:     int64(sv.Port),
			Label:    nullString(sv.Label),
			Model:    nullString(sv.Model),
			SeenAt:   p.at,
		})
		if err != nil {
			return fmt.Errorf("record service %s of device %d: %w", sv.Type, deviceID, err)
		}
	}

	return nil
}
