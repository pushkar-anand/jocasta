package plugin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/pkg/technitium"
)

// technitiumPrefix puts an instance key in a namespace. So it cannot collide
// with the key of a different source kind.
const technitiumPrefix = "technitium:"

const (
	// logPageSize and maxLogPages limit one read to 20,000 lookups. So a log
	// with no read for days empties over several reads.
	logPageSize = 1000
	maxLogPages = 20

	// firstReadSpan is how far back a read with no cursor starts.
	firstReadSpan = time.Hour
)

// Technitium reads the query log of one Technitium DNS Server.
//
// The server logs queries only through an installed app. So each read first
// asks which app logs them. This question costs one small request each time.
// It follows the installation or removal of an app without a restart.
type Technitium struct {
	name   string
	client *technitium.Technitium
	logger *slog.Logger

	// logApp is the app to read when more than one app logs queries. It is
	// empty for the only app.
	logApp string

	// now is a field, so tests can fix where a first read starts.
	now func() time.Time
}

var (
	// ErrNoTechnitiumName refuses a missing name. The name is a database key.
	// With one default, the lookups of two servers would go under one
	// source.
	ErrNoTechnitiumName = errors.New("plugin: technitium instance has no name")

	// ErrNoQueryLogger is a server with no installed app that logs queries, or
	// no app with the name in log_app.
	ErrNoQueryLogger = errors.New("plugin: no installed app logs queries")

	// ErrSeveralQueryLoggers is a server with more than one app that logs
	// queries, and no log_app to choose one.
	ErrSeveralQueryLoggers = errors.New("plugin: several installed apps log queries")
)

// NewTechnitium builds the plugin for one server. name is the instance key from
// config, such as "resolver". logApp is the app to read. It is empty when the
// server has one app that logs queries.
//
// It does no I/O. So if the server is down at startup, the server still starts
// and tries again later.
func NewTechnitium(name string, client *technitium.Technitium, logApp string, log *slog.Logger) (*Technitium, error) {
	if name == "" {
		return nil, ErrNoTechnitiumName
	}

	if client == nil {
		return nil, fmt.Errorf("plugin: technitium %q has no client", name)
	}

	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &Technitium{
		name:   technitiumPrefix + name,
		client: client,
		logger: log.With(slog.String("plugin", technitiumPrefix+name)),
		logApp: logApp,
		now:    time.Now,
	}, nil
}

// Name returns the configured name of the plugin.
func (t *Technitium) Name() string { return t.name }

// Kind identifies this plugin as a DNS source.
func (t *Technitium) Kind() dbtype.SourceKind { return dbtype.SourceDNS }

// Queries reads the log from cursor. Its cursor is the timestamp of the last
// read entry, and how many entries have that timestamp. Together, they name a
// place in the log that later entries cannot move.
func (t *Technitium) Queries(ctx context.Context, cursor string) ([]Query, string, error) {
	from, skip, err := parseLogCursor(cursor)
	if err != nil {
		// A cursor that does not parse would refuse every later read. So the
		// read starts again, and one hour can count twice.
		t.logger.WarnContext(ctx, "stored cursor does not parse; reading the last hour", logger.Err(err))

		from, skip = time.Time{}, 0
	}

	if from.IsZero() {
		from = t.now().Add(-firstReadSpan).UTC()
	}

	app, classPath, err := t.queryLogger(ctx)
	if err != nil {
		return nil, cursor, err
	}

	var out []Query

	// The server returns entries from a start time, inclusive. So the entries
	// at the timestamp of the cursor, from an earlier read, come back first.
	// Queries skips them.
	last, taken := from, skip

	for page := 1; page <= maxLogPages; page++ {
		res, err := t.client.QueryLogs(ctx, technitium.LogQuery{
			App:       app,
			ClassPath: classPath,
			Start:     from,
			Page:      page,
			PerPage:   logPageSize,
		})
		if err != nil {
			return out, formatLogCursor(last, taken), fmt.Errorf("read query log page %d: %w", page, classifyTechnitium(err))
		}

		for _, e := range res.Entries {
			at, err := parseLogTime(e.Timestamp)
			if err != nil {
				t.logger.DebugContext(ctx, "ignoring log entry with an unusable timestamp", logger.Err(err))

				continue
			}

			if at.Equal(from) && skip > 0 {
				skip--

				continue
			}

			switch {
			case at.After(last):
				last, taken = at, 1
			case at.Equal(last):
				taken++
			}

			if q, ok := technitiumQuery(e, at); ok {
				out = append(out, q)
			}
		}

		if int64(page) >= res.TotalPages {
			break
		}
	}

	return out, formatLogCursor(last, taken), nil
}

// queryLogger returns the app and class path of the log that Queries reads.
func (t *Technitium) queryLogger(ctx context.Context) (app, classPath string, err error) {
	apps, err := t.client.Apps(ctx)
	if err != nil {
		return "", "", fmt.Errorf("list apps: %w", classifyTechnitium(err))
	}

	var names []string

	for _, a := range apps {
		if t.logApp != "" && a.Name != t.logApp {
			continue
		}

		for _, d := range a.DNSApps {
			if !d.IsQueryLogger {
				continue
			}

			if app == "" {
				app, classPath = a.Name, d.ClassPath
			}

			names = append(names, a.Name)
		}
	}

	switch {
	case app == "" && t.logApp != "":
		return "", "", fmt.Errorf("%w: no app named %q; check log_app against the server's Apps page", ErrNoQueryLogger, t.logApp)
	case app == "":
		return "", "", fmt.Errorf("%w: install one, such as Query Logs (Sqlite), from the server's Apps page", ErrNoQueryLogger)
	case t.logApp == "" && len(names) > 1:
		return "", "", fmt.Errorf("%w: %s; set log_app to the one to read", ErrSeveralQueryLoggers, strings.Join(names, ", "))
	}

	return app, classPath, nil
}

// technitiumQuery maps one log entry. It reports false for an entry that names
// no client or no name, such as a query for the root.
func technitiumQuery(e technitium.LogEntry, at time.Time) (Query, bool) {
	client, err := netip.ParseAddr(e.ClientIPAddress)
	if err != nil {
		return Query{}, false
	}

	name := strings.ToLower(strings.TrimSuffix(e.QName, "."))
	if name == "" {
		return Query{}, false
	}

	var blocked bool

	switch e.ResponseType {
	case "Blocked", "UpstreamBlocked", "CacheBlocked":
		blocked = true
	}

	return Query{
		Client:  client.Unmap(),
		Name:    name,
		Type:    e.QType,
		Blocked: blocked,
		Failed:  !blocked && (e.RCode == "NxDomain" || e.RCode == "ServerFailure"),
		Answers: answerAddrs(e.Answer),
		At:      at,
	}, true
}

// answerAddrs returns the addresses in an answer that the server logged as
// "CNAME edge.example.net, A 192.0.2.1". It leaves out records that are not
// addresses.
func answerAddrs(answer string) []netip.Addr {
	var out []netip.Addr

	for record := range strings.SplitSeq(answer, ", ") {
		// Older servers logged only the data, with no type in front.
		_, data, ok := strings.Cut(record, " ")
		if !ok {
			data = record
		}

		if a, err := netip.ParseAddr(data); err == nil {
			out = append(out, a.Unmap())
		}
	}

	return out
}

// parseLogTime reads the timestamp of a log entry. The server stores UTC. A
// timestamp without a zone is UTC.
func parseLogTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC(), nil
	}

	t, err := time.ParseInLocation("2006-01-02T15:04:05.999999999", s, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("log timestamp %q: %w", s, err)
	}

	return t, nil
}

// formatLogCursor and parseLogCursor write and read a cursor as
// "<RFC 3339 timestamp>|<entries taken at it>". An empty cursor parses as the
// zero time.
func formatLogCursor(at time.Time, taken int) string {
	return at.UTC().Format(time.RFC3339Nano) + "|" + strconv.Itoa(taken)
}

func parseLogCursor(cursor string) (time.Time, int, error) {
	if cursor == "" {
		return time.Time{}, 0, nil
	}

	ts, n, ok := strings.Cut(cursor, "|")
	if !ok {
		return time.Time{}, 0, fmt.Errorf("cursor %q has no count", cursor)
	}

	at, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("cursor %q: %w", cursor, err)
	}

	taken, err := strconv.Atoi(n)
	if err != nil || taken < 0 {
		return time.Time{}, 0, fmt.Errorf("cursor %q has no count", cursor)
	}

	return at.UTC(), taken, nil
}

// classifyTechnitium maps the errors of the client to the errors of this
// package. So no caller needs to import pkg/technitium to tell a failure that
// a retry can fix from one that needs a person.
func classifyTechnitium(err error) error {
	switch {
	case errors.Is(err, technitium.ErrUnauthorized):
		return fmt.Errorf("%w: %w", ErrAuth, err)
	case errors.Is(err, technitium.ErrUnreachable), errors.Is(err, technitium.ErrTLS):
		return fmt.Errorf("%w: %w", ErrUnreachable, err)
	default:
		return err
	}
}

var (
	_ Plugin      = (*Technitium)(nil)
	_ QueryReader = (*Technitium)(nil)
)
