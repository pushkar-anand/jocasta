package inventory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
)

// arrive marks a device that answered this reading as present. A quiet device
// comes back with this sighting. A watched device logs it with the sighting
// before, which is when it went quiet.
//
// d is the row from before this reading changed it. So its last_seen is still
// the last sighting.
func (s *Store) arrive(ctx context.Context, p *pass, d *models.Device) error {
	if d.PresentSince.Valid {
		return nil
	}

	if err := p.q.MarkPresent(ctx, models.MarkPresentParams{PresentSince: dbtype.NullTime{Time: p.at, Valid: true}, ID: d.ID}); err != nil {
		return fmt.Errorf("mark device %d present: %w", d.ID, err)
	}

	d.PresentSince = dbtype.NullTime{Time: p.at, Valid: true}

	// Only a sighting older than the window ends a quiet period. depart uses
	// the same test. A device that this reading created was last seen now. A
	// device recorded before presence tracking can have a sighting minutes
	// ago. Neither was quiet.
	if !d.LastSeen.Before(p.at.Add(-s.onlineWindow)) {
		return nil
	}

	p.res.Back++

	if !d.IsWatched {
		return nil
	}

	return s.event(ctx, p, d.ID, dbtype.EventDeviceBack, d.LastSeen.Format(time.RFC3339), "", "")
}

// present is one present device that a reading could see, with its address for
// the test against the prefix of a sweep.
type present struct {
	id       int64
	lastSeen dbtype.Time
	watched  bool
}

// depart marks as quiet every present device that this reading could see, but
// did not see. Also, no source saw it for longer than the online window. A
// sweep could see the devices with a current address in its prefix. A source
// that reads every network could see the devices of its claims.
//
// Absence counts only where a reading looked. So a device on a network that no
// reading covers stays present. After downtime, a restart marks as quiet only
// the devices that still do not answer when their network is read again. The
// window uses last_seen, which every source updates. So a device that the
// router saw a minute ago stays present when a sweep misses it.
func (s *Store) depart(ctx context.Context, p *pass, r reading) error {
	candidates, err := s.couldHaveSeen(ctx, p, r)
	if err != nil {
		return err
	}

	cutoff := p.at.Add(-s.onlineWindow)

	for _, c := range candidates {
		if _, seen := p.seen[c.id]; seen || !c.lastSeen.Before(cutoff) {
			continue
		}

		if err := p.q.MarkQuiet(ctx, c.id); err != nil {
			return fmt.Errorf("mark device %d quiet: %w", c.id, err)
		}

		p.res.Quiet++

		if !c.watched {
			continue
		}

		if err := s.event(ctx, p, c.id, dbtype.EventDeviceQuiet, c.lastSeen.Format(time.RFC3339), "", ""); err != nil {
			return err
		}
	}

	return nil
}

// couldHaveSeen returns the present devices that r could see, each once, in id
// order.
func (s *Store) couldHaveSeen(ctx context.Context, p *pass, r reading) ([]present, error) {
	if r.network == nil {
		rows, err := p.q.PresentOfSource(ctx, p.sourceID)
		if err != nil {
			return nil, fmt.Errorf("present devices of source %d: %w", p.sourceID, err)
		}

		out := make([]present, len(rows))
		for i, row := range rows {
			out[i] = present{id: row.ID, lastSeen: row.LastSeen, watched: row.IsWatched}
		}

		return out, nil
	}

	rows, err := p.q.PresentWithAddresses(ctx)
	if err != nil {
		return nil, fmt.Errorf("present devices: %w", err)
	}

	// Rows come one device at a time, one row for each current address. So a
	// device with two addresses in the prefix stays once.
	var out []present

	for _, row := range rows {
		if !r.network.Contains(row.IP.Addr) {
			continue
		}

		if n := len(out); n > 0 && out[n-1].id == row.ID {
			continue
		}

		out = append(out, present{id: row.ID, lastSeen: row.LastSeen, watched: row.IsWatched})
	}

	return out, nil
}

// Watch sets whether the owner gets told when a device goes quiet or comes
// back. It records the change in the log as an edit. The watch belongs to the
// owner, as a label does. No scan changes it.
//
// It is set alone, outside [Curation]. A curation replaces every field. A form
// or tool from before the watch feature would clear it.
func (s *Store) Watch(ctx context.Context, id int64, watched bool) (*Device, error) {
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin watch: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	q := s.q.WithTx(tx)

	before, err := q.GetDevice(ctx, id)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("device %d: %w", id, ErrNotFound)
	case err != nil:
		return nil, fmt.Errorf("device %d: %w", id, err)
	}

	if before.IsWatched != watched {
		if _, err := q.SetDeviceWatched(ctx, models.SetDeviceWatchedParams{IsWatched: watched, ID: id}); err != nil {
			return nil, fmt.Errorf("watch device %d: %w", id, err)
		}

		params := models.CreateEventParams{
			DeviceID:   sql.NullInt64{Int64: id, Valid: true},
			Kind:       dbtype.EventDeviceEdited,
			OldValue:   nullString(yesNo(before.IsWatched)),
			NewValue:   nullString(yesNo(watched)),
			Detail:     nullString("watched"),
			OccurredAt: s.stamp(),
		}

		if err := q.CreateEvent(ctx, params); err != nil {
			return nil, fmt.Errorf("record watch of device %d: %w", id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit watch: %w", err)
	}

	return s.Device(ctx, id)
}

// presenceChange writes the text of a presence event. This is how long the
// device was without an answer when it went quiet. Or it is how long it was
// quiet when it came back. Both start at the sighting of the event, and end
// when the event was logged.
func presenceChange(e *Event) string {
	last, err := time.Parse(time.RFC3339, e.OldValue)
	if err != nil {
		return ""
	}

	span := spanWords(e.At.Sub(last))

	if e.Kind == dbtype.EventDeviceQuiet {
		return "after " + span + " without an answer"
	}

	return "after " + span + " quiet"
}

// spanWords writes a length of time with the precision of a person: minutes
// below an hour, hours below two days, and days after that.
func spanWords(d time.Duration) string {
	switch {
	case d < time.Hour:
		return count(int(d.Minutes()), "minute")
	case d < 48*time.Hour:
		return count(int(d.Hours()), "hour")
	}

	return count(int(d.Hours()/24), "day")
}

// count writes n of unit, in the singular for one.
func count(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}

	return strconv.Itoa(n) + " " + unit + "s"
}
