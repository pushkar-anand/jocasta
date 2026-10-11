package plugin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/pkg/adguard"
)

// adguardPrefix namespaces an instance key so it cannot collide with another
// source kind's.
const adguardPrefix = "adguard:"

// AdGuard reads the query log of one AdGuard Home.
type AdGuard struct {
	name   string
	client *adguard.AdGuard
	logger *slog.Logger

	// now is a field so tests can pin where a first read starts.
	now func() time.Time
}

// ErrNoAdGuardName refuses a missing name: the name is a database key, and
// one default would file two servers' lookups under one source.
var ErrNoAdGuardName = errors.New("plugin: adguard instance has no name")

// NewAdGuard builds the plugin for one AdGuard Home. name is the instance key
// from config, such as "resolver".
//
// It performs no I/O, so a server that is down at startup is retried later
// while the server still starts.
func NewAdGuard(name string, client *adguard.AdGuard, log *slog.Logger) (*AdGuard, error) {
	if name == "" {
		return nil, ErrNoAdGuardName
	}

	if client == nil {
		return nil, fmt.Errorf("plugin: adguard %q has no client", name)
	}

	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &AdGuard{
		name:   adguardPrefix + name,
		client: client,
		logger: log.With(slog.String("plugin", adguardPrefix+name)),
		now:    time.Now,
	}, nil
}

// Name returns the plugin's configured name.
func (a *AdGuard) Name() string { return a.name }

// Kind identifies this plugin as a DNS source.
func (a *AdGuard) Kind() dbtype.SourceKind { return dbtype.SourceDNS }

// Queries reads the log from cursor, which is the time of the newest entry
// read. AdGuard Home pages its log newest first, so a read that reaches its
// page cap before the cursor keeps the newest lookups and skips the older
// ones. A page that fails returns nothing and the cursor it was given: what
// came before it sits above lookups no read has reached.
//
// Two lookups logged in the same nanosecond, one before a read and one
// after, count once.
func (a *AdGuard) Queries(ctx context.Context, cursor string) ([]Query, string, error) {
	after, err := parseTimeCursor(cursor)
	if err != nil {
		// A cursor that does not parse would refuse every read from here on,
		// so the read starts afresh, at the cost of an hour counted twice.
		a.logger.WarnContext(ctx, "stored cursor does not parse; reading the last hour", logger.Err(err))

		after = time.Time{}
	}

	stop := after
	if stop.IsZero() {
		stop = a.now().Add(-firstReadSpan).UTC()
	}

	var out []Query

	newest := after
	req := adguard.LogRequest{Limit: logPageSize}

	for page := range maxLogPages {
		res, err := a.client.QueryLog(ctx, req)
		if err != nil {
			return nil, cursor, fmt.Errorf("read query log page %d: %w", page+1, classifyAdGuard(err))
		}

		// The server may page by fewer entries than asked for, so only an
		// empty page says the log is done.
		if len(res.Data) == 0 {
			return oldestFirst(out), formatTimeCursor(newest), nil
		}

		for _, e := range res.Data {
			at, err := time.Parse(time.RFC3339Nano, e.Time)
			if err != nil {
				a.logger.DebugContext(ctx, "ignoring log entry with an unusable timestamp", logger.Err(err))

				continue
			}

			at = at.UTC()

			if !at.After(stop) {
				return oldestFirst(out), formatTimeCursor(newest), nil
			}

			if at.After(newest) {
				newest = at
			}

			req.OlderThan = at

			if q, ok := adguardQuery(e, at); ok {
				out = append(out, q)
			}
		}
	}

	if !after.IsZero() {
		a.logger.WarnContext(ctx, "more lookups were logged since the last read than one read takes; skipping the older ones",
			slog.Int("kept", len(out)))
	}

	return oldestFirst(out), formatTimeCursor(newest), nil
}

// adguardQuery maps one log entry, and reports false for an entry that names
// no client or no name.
func adguardQuery(e adguard.LogEntry, at time.Time) (Query, bool) {
	client, err := netip.ParseAddr(e.Client)
	if err != nil {
		return Query{}, false
	}

	name := strings.ToLower(strings.TrimSuffix(e.Question.Name, "."))
	if name == "" {
		return Query{}, false
	}

	var blocked bool

	switch e.Reason {
	case "FilteredBlackList", "FilteredSafeBrowsing", "FilteredParental", "FilteredBlockedService":
		blocked = true
	}

	var answers []netip.Addr

	for _, r := range e.Answer {
		if r.Type != "A" && r.Type != "AAAA" {
			continue
		}

		if addr, err := netip.ParseAddr(r.Value); err == nil {
			answers = append(answers, addr.Unmap())
		}
	}

	return Query{
		Client:  client.Unmap(),
		Name:    name,
		Type:    e.Question.Type,
		Blocked: blocked,
		Failed:  !blocked && (e.Status == "NXDOMAIN" || e.Status == "SERVFAIL"),
		Answers: answers,
		At:      at,
	}, true
}

// formatTimeCursor and parseTimeCursor write and read a cursor as an RFC 3339
// timestamp. An empty cursor parses as the zero time.
func formatTimeCursor(at time.Time) string {
	if at.IsZero() {
		return ""
	}

	return at.UTC().Format(time.RFC3339Nano)
}

func parseTimeCursor(cursor string) (time.Time, error) {
	if cursor == "" {
		return time.Time{}, nil
	}

	at, err := time.Parse(time.RFC3339Nano, cursor)
	if err != nil {
		return time.Time{}, fmt.Errorf("cursor %q: %w", cursor, err)
	}

	return at.UTC(), nil
}

// classifyAdGuard maps the client's errors onto this package's, so nothing
// above has to import pkg/adguard to tell a retryable failure from one that
// needs a human.
func classifyAdGuard(err error) error {
	switch {
	case errors.Is(err, adguard.ErrUnauthorized):
		return fmt.Errorf("%w: %w", ErrAuth, err)
	case errors.Is(err, adguard.ErrUnreachable), errors.Is(err, adguard.ErrTLS):
		return fmt.Errorf("%w: %w", ErrUnreachable, err)
	default:
		return err
	}
}

var (
	_ Plugin      = (*AdGuard)(nil)
	_ QueryReader = (*AdGuard)(nil)
)
