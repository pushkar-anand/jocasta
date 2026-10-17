package plugin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/pkg/pihole"
)

// piholePrefix puts an instance key in a namespace. So it cannot collide
// with the key of a different source kind.
const piholePrefix = "pihole:"

// PiHole reads the query log of one Pi-hole v6.
type PiHole struct {
	name   string
	client *pihole.PiHole
	logger *slog.Logger

	// now is a field, so tests can fix where a first read starts.
	now func() time.Time
}

// ErrNoPiHoleName refuses a missing name. The name is a database key. With one
// default, the lookups of two servers would go under one source.
var ErrNoPiHoleName = errors.New("plugin: pihole instance has no name")

// NewPiHole builds the plugin for one Pi-hole. name is the instance key from
// config, such as "resolver".
//
// It does no I/O. So if the Pi-hole is down at startup, the server still starts
// and tries again later.
func NewPiHole(name string, client *pihole.PiHole, log *slog.Logger) (*PiHole, error) {
	if name == "" {
		return nil, ErrNoPiHoleName
	}

	if client == nil {
		return nil, fmt.Errorf("plugin: pihole %q has no client", name)
	}

	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &PiHole{
		name:   piholePrefix + name,
		client: client,
		logger: log.With(slog.String("plugin", piholePrefix+name)),
		now:    time.Now,
	}, nil
}

// Name returns the configured name of the plugin.
func (p *PiHole) Name() string { return p.name }

// Kind identifies this plugin as a DNS source.
func (p *PiHole) Kind() dbtype.SourceKind { return dbtype.SourceDNS }

// Queries reads the log from cursor. The cursor is the id of the newest query
// that a read took. Pi-hole pages its log newest first. So if a read reaches
// its page limit before the cursor, it keeps the newest lookups and skips the
// older ones. A page that fails returns nothing, with the cursor that the read
// got. The pages before it are above lookups that no read reached. If the
// newest id of a log is below the cursor, the log was reset. Queries then
// reads it as a first read.
//
// Pi-hole logs no answer addresses. So its lookups name no traffic peers.
func (p *PiHole) Queries(ctx context.Context, cursor string) ([]Query, string, error) {
	after, err := parseIDCursor(cursor)
	if err != nil {
		// A cursor that does not parse would refuse every later read. So the
		// read starts again, and one hour can count twice.
		p.logger.WarnContext(ctx, "stored cursor does not parse; reading the last hour", logger.Err(err))

		after = 0
	}

	req := pihole.QueryRequest{Length: logPageSize}
	if after == 0 {
		req.From = p.now().Add(-firstReadSpan)
	}

	var out []Query

	newest := after

	for page := range maxLogPages {
		req.Start = page * logPageSize

		res, err := p.client.Queries(ctx, req)
		if err != nil {
			return nil, cursor, fmt.Errorf("read query log page %d: %w", page+1, classifyPiHole(err))
		}

		// When its database is gone, Pi-hole numbers its log from zero again.
		// An example is a container deployed again without its volume. Then
		// every new query is below the cursor. Without this code, the source
		// records nothing until the new ids pass the old ids. With no cursor,
		// after must be 0. So this recursion happens at most once.
		if page == 0 && after != 0 && len(res.Queries) > 0 && res.Queries[0].ID < after {
			p.logger.WarnContext(ctx, "query log is numbered below the stored cursor, so the log was reset; reading the last hour",
				slog.Int64("cursor", after),
				slog.Int64("newest", res.Queries[0].ID),
			)

			return p.Queries(ctx, "")
		}

		for _, e := range res.Queries {
			if e.ID <= after {
				return oldestFirst(out), formatIDCursor(newest), nil
			}

			// The later pages count their offset from the newest query of the
			// first page. So queries logged during the read cannot move
			// them.
			if req.Cursor == 0 {
				req.Cursor = e.ID
			}

			newest = max(newest, e.ID)

			if q, ok := piholeQuery(e); ok {
				out = append(out, q)
			}
		}

		if len(res.Queries) < logPageSize {
			return oldestFirst(out), formatIDCursor(newest), nil
		}
	}

	if after != 0 {
		p.logger.WarnContext(ctx, "more lookups were logged since the last read than one read takes; skipping the older ones",
			slog.Int("kept", len(out)))
	}

	return oldestFirst(out), formatIDCursor(newest), nil
}

// oldestFirst reverses a read that came newest first.
func oldestFirst(qs []Query) []Query {
	slices.Reverse(qs)

	return qs
}

// piholeQuery maps one logged query. It reports false for a query that names no
// client or no name.
func piholeQuery(e pihole.Query) (Query, bool) {
	client, err := netip.ParseAddr(e.Client.IP)
	if err != nil {
		return Query{}, false
	}

	name := strings.ToLower(strings.TrimSuffix(e.Domain, "."))
	if name == "" {
		return Query{}, false
	}

	var blocked bool

	switch e.Status {
	case "GRAVITY", "REGEX", "DENYLIST",
		"EXTERNAL_BLOCKED_IP", "EXTERNAL_BLOCKED_NULL", "EXTERNAL_BLOCKED_NXRA", "EXTERNAL_BLOCKED_EDE15",
		"GRAVITY_CNAME", "REGEX_CNAME", "DENYLIST_CNAME",
		"SPECIAL_DOMAIN", "DBBUSY":
		blocked = true
	}

	return Query{
		Client:  client.Unmap(),
		Name:    name,
		Type:    e.Type,
		Blocked: blocked,
		Failed:  !blocked && (e.Reply.Type == "NXDOMAIN" || e.Reply.Type == "SERVFAIL"),
		At:      e.At(),
	}, true
}

// formatIDCursor and parseIDCursor write and read a cursor as a query id. An
// empty cursor parses as 0, which no query has.
func formatIDCursor(id int64) string {
	if id == 0 {
		return ""
	}

	return strconv.FormatInt(id, 10)
}

func parseIDCursor(cursor string) (int64, error) {
	if cursor == "" {
		return 0, nil
	}

	id, err := strconv.ParseInt(cursor, 10, 64)
	if err != nil || id < 0 {
		return 0, fmt.Errorf("cursor %q is no query id", cursor)
	}

	return id, nil
}

// classifyPiHole maps the errors of the client to the errors of this
// package. So no caller needs to import pkg/pihole to tell a failure that a
// retry can fix from one that needs a person.
func classifyPiHole(err error) error {
	switch {
	case errors.Is(err, pihole.ErrUnauthorized):
		return fmt.Errorf("%w: %w", ErrAuth, err)
	case errors.Is(err, pihole.ErrUnreachable), errors.Is(err, pihole.ErrTLS):
		return fmt.Errorf("%w: %w", ErrUnreachable, err)
	default:
		return err
	}
}

var (
	_ Plugin      = (*PiHole)(nil)
	_ QueryReader = (*PiHole)(nil)
)
