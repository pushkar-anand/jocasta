package inventory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
	"github.com/pushkar-anand/jocasta/internal/plugin"
	"github.com/pushkar-anand/jocasta/internal/topology"
)

// RecordTopology files what one source says is plugged into it, in one
// transaction. The source's ports and neighbours are replaced; its sightings
// are added to, so an address the source no longer lists keeps its last
// sighting until the prune.
func (s *Store) RecordTopology(ctx context.Context, source string, kind dbtype.SourceKind, t plugin.Topology) error {
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin topology: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	q := s.q.WithTx(tx)

	at := s.stamp()
	if !t.ReadAt.IsZero() {
		at = dbtype.NewTime(t.ReadAt)
	}

	src, err := q.UpsertSource(ctx, models.UpsertSourceParams{Kind: kind, Name: source, CreatedAt: at})
	if err != nil {
		return fmt.Errorf("upsert source %q: %w", source, err)
	}

	err = q.UpsertTopologyNode(ctx, models.UpsertTopologyNodeParams{
		SourceID: src.ID,
		Identity: nullString(t.Identity),
		Gateway:  t.Gateway,
		OwnMacs:  strings.Join(t.Own, ","),
		ReadAt:   at,
	})
	if err != nil {
		return fmt.Errorf("upsert topology node %q: %w", source, err)
	}

	if err := replaceTopologyPorts(ctx, q, src.ID, t.Ports); err != nil {
		return err
	}

	if err := replaceNeighbours(ctx, q, src.ID, t.Neighbours); err != nil {
		return err
	}

	for _, seen := range t.Seen {
		err := q.UpsertTopologySighting(ctx, models.UpsertTopologySightingParams{
			SourceID:  src.ID,
			Port:      seen.Port,
			MAC:       seen.MAC,
			Vlan:      int64(seen.VLAN),
			Wifi:      seen.WiFi,
			Ssid:      nullString(seen.SSID),
			Band:      nullString(seen.Band),
			FirstSeen: at,
			LastSeen:  at,
		})
		if err != nil {
			return fmt.Errorf("upsert sighting %s on %s: %w", seen.MAC, seen.Port, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit topology: %w", err)
	}

	return nil
}

// replaceTopologyPorts replaces a source's ports.
func replaceTopologyPorts(ctx context.Context, q *models.Queries, sourceID int64, ports []plugin.TopologyPort) error {
	if err := q.DeleteTopologyPorts(ctx, sourceID); err != nil {
		return fmt.Errorf("clear ports: %w", err)
	}

	for _, p := range ports {
		err := q.InsertTopologyPort(ctx, models.InsertTopologyPortParams{
			SourceID: sourceID,
			Name:     p.Name,
			Kind:     string(p.Kind),
			Pvid:     int64(p.PVID),
			Tagged:   joinVLANs(p.Tagged),
			Untagged: joinVLANs(p.Untagged),
			Running:  p.Running,
		})
		if err != nil {
			return fmt.Errorf("insert port %s: %w", p.Name, err)
		}
	}

	return nil
}

// replaceNeighbours replaces a source's neighbours.
func replaceNeighbours(ctx context.Context, q *models.Queries, sourceID int64, ns []plugin.Neighbour) error {
	if err := q.DeleteTopologyNeighbours(ctx, sourceID); err != nil {
		return fmt.Errorf("clear neighbours: %w", err)
	}

	for _, n := range ns {
		var addr string
		if n.Addr.IsValid() {
			addr = n.Addr.String()
		}

		err := q.InsertTopologyNeighbour(ctx, models.InsertTopologyNeighbourParams{
			SourceID:  sourceID,
			Port:      n.Port,
			MAC:       n.MAC,
			Identity:  n.Identity,
			Platform:  nullString(n.Platform),
			Board:     nullString(n.Board),
			TheirPort: nullString(n.TheirPort),
			Address:   nullString(addr),
		})
		if err != nil {
			return fmt.Errorf("insert neighbour %s on %s: %w", n.MAC, n.Port, err)
		}
	}

	return nil
}

// LastTopologyReadAt returns when any source was last read for what is
// plugged into it. It returns ErrNotFound when none has been.
func (s *Store) LastTopologyReadAt(ctx context.Context) (time.Time, error) {
	at, err := s.q.LastTopologyReadAt(ctx)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return time.Time{}, fmt.Errorf("last topology read: %w", ErrNotFound)
	case err != nil:
		return time.Time{}, fmt.Errorf("last topology read: %w", err)
	}

	return at.Time, nil
}

// joinVLANs renders a set of VLANs as the comma-separated list the ports table
// stores.
func joinVLANs(vlans []int) string {
	parts := make([]string, len(vlans))
	for i, v := range vlans {
		parts[i] = strconv.Itoa(v)
	}

	return strings.Join(parts, ",")
}

// Topology returns the network's tree as each source last described it, with
// the devices that are not ignored placed in it.
func (s *Store) Topology(ctx context.Context) (*topology.Tree, error) {
	nodes, err := s.q.ListTopologyNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list topology nodes: %w", err)
	}

	ports, err := s.q.ListTopologyPorts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list topology ports: %w", err)
	}

	sightings, err := s.q.ListTopologySightings(ctx)
	if err != nil {
		return nil, fmt.Errorf("list topology sightings: %w", err)
	}

	neighbours, err := s.q.ListTopologyNeighbours(ctx)
	if err != nil {
		return nil, fmt.Errorf("list topology neighbours: %w", err)
	}

	devices, err := s.ListDevices(ctx, DeviceFilter{})
	if err != nil {
		return nil, err
	}

	sources := make([]topology.Source, 0, len(nodes))
	index := make(map[int64]int, len(nodes))

	for _, n := range nodes {
		index[n.SourceID] = len(sources)

		var own []string
		if n.OwnMacs != "" {
			own = strings.Split(n.OwnMacs, ",")
		}

		sources = append(sources, topology.Source{
			ID:       n.SourceID,
			Name:     n.SourceName,
			Identity: n.Identity.String,
			Gateway:  n.Gateway,
			Own:      own,
			ReadAt:   n.ReadAt.Time,
		})
	}

	for _, p := range ports {
		if i, ok := index[p.SourceID]; ok {
			sources[i].Ports = append(sources[i].Ports, topology.Port{
				Name:     p.Name,
				Kind:     topology.PortKind(p.Kind),
				PVID:     int(p.Pvid),
				Tagged:   splitVLANs(p.Tagged),
				Untagged: splitVLANs(p.Untagged),
			})
		}
	}

	for _, seen := range sightings {
		if i, ok := index[seen.SourceID]; ok {
			sources[i].Seen = append(sources[i].Seen, topology.Sighting{
				Port:     seen.Port,
				MAC:      seen.MAC,
				VLAN:     int(seen.Vlan),
				WiFi:     seen.Wifi,
				SSID:     seen.Ssid.String,
				Band:     seen.Band.String,
				LastSeen: seen.LastSeen.Time,
			})
		}
	}

	for _, n := range neighbours {
		if i, ok := index[n.SourceID]; ok {
			sources[i].Neighbours = append(sources[i].Neighbours, topology.Neighbour{
				Port:     n.Port,
				MAC:      n.MAC,
				Identity: n.Identity,
				Board:    n.Board.String,
			})
		}
	}

	return topology.Build(sources, placeable(devices)), nil
}

// placeable turns the devices with a hardware address into what the tree
// places. A device's VLAN is its first network's.
func placeable(devices []*Device) []topology.Device {
	out := make([]topology.Device, 0, len(devices))

	for _, d := range devices {
		if d.MAC == "" {
			continue
		}

		td := topology.Device{ID: d.ID, MAC: d.MAC, Name: d.Name(), Class: string(d.Class), Online: d.Online}

		for _, n := range d.Networks {
			if n.VLAN > 0 {
				td.VLAN = n.VLAN

				break
			}
		}

		out = append(out, td)
	}

	return out
}

// splitVLANs reads a list joinVLANs wrote.
func splitVLANs(s string) []int {
	var out []int

	for part := range strings.SplitSeq(s, ",") {
		if n, err := strconv.Atoi(part); err == nil {
			out = append(out, n)
		}
	}

	return out
}
