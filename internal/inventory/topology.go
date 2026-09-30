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
