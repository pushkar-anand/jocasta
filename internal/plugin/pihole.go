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

// piholePrefix namespaces an instance key so it cannot collide with another
// source kind's.
const piholePrefix = "pihole:"

// PiHole reads the query log of one Pi-hole v6.
type PiHole struct {
	name   string
	client *pihole.PiHole
	logger *slog.Logger

	// now is a field so tests can pin where a first read starts.
	now func() time.Time
}

// ErrNoPiHoleName refuses a missing name: the name is a database key, and one
// default would file two servers' lookups under one source.
var ErrNoPiHoleName = errors.New("plugin: pihole instance has no name")

// NewPiHole builds the plugin for one Pi-hole. name is the instance key from
// config, such as "resolver".
//
// It performs no I/O, so a Pi-hole that is down at startup is retried later
// while the server still starts.
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

// Name returns the plugin's configured name.
func (p *PiHole) Name() string { return p.name }

// Kind identifies this plugin as a DNS source.
func (p *PiHole) Kind() dbtype.SourceKind { return dbtype.SourceDNS }

// Queries reads the log from cursor, which is the id of the newest query
// read. Pi-hole pages its log newest first, so a read that reaches its page
// cap before the cursor keeps the newest lookups and skips the older ones.
// A page that fails returns nothing and the cursor it was given: what came
// before it sits above lookups no read has reached.
//
// Pi-hole logs no answer addresses, so its lookups name no traffic peers.
func (p *PiHole) Queries(ctx context.Context, cursor string) ([]Query, string, error) {
	after, err := parseIDCursor(cursor)
	if err != nil {
		// A cursor that does not parse would refuse every read from here on,
		// so the read starts afresh, at the cost of an hour counted twice.
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

		for _, e := range res.Queries {
			if e.ID <= after {
				return oldestFirst(out), formatIDCursor(newest), nil
			}

			// The later pages count their offset from the first page's
			// newest query, so queries logged during the read cannot shift
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

// oldestFirst reverses a read taken newest first.
func oldestFirst(qs []Query) []Query {
	slices.Reverse(qs)

	return qs
}

// piholeQuery maps one logged query, and reports false for one that names no
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

// classifyPiHole maps the client's errors onto this package's, so nothing
// above has to import pkg/pihole to tell a retryable failure from one that
// needs a human.
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
