package notify

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/db/models"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// queueSize is how many finished scans can wait to be sent. Scans finish
// minutes apart. So the queue fills only while every send times out. If a scan
// finds the queue full, the notifier logs it and does not send it.
const queueSize = 64

// Notifier sends the changes of each finished scan to the destinations that
// asked for them. Queue gives it scans, and Run sends them on its own
// goroutine. So a slow or unreachable service never delays a scan.
//
// The notifier does not send scans that still wait when the server stops.
type Notifier struct {
	conn         *sql.DB
	q            *models.Queries
	store        *inventory.Store
	destinations []*Destination
	queue        chan int64
	log          *slog.Logger

	mu   sync.Mutex
	last map[string]Result
}

// Result is the outcome of the last message to one destination.
type Result struct {
	At  time.Time
	Err error // nil when it was delivered
}

// New returns a notifier that reads the changes of each scan from store. It
// keeps the chosen kinds of each destination in conn.
func New(conn *sql.DB, store *inventory.Store, log *slog.Logger, destinations ...*Destination) *Notifier {
	return &Notifier{
		conn:         conn,
		q:            models.New(conn),
		store:        store,
		destinations: destinations,
		queue:        make(chan int64, queueSize),
		log:          log,
		last:         make(map[string]Result),
	}
}

// Queue gives a finished scan to Run. It never blocks.
func (n *Notifier) Queue(ctx context.Context, scanID int64) {
	select {
	case n.queue <- scanID:
	default:
		n.log.WarnContext(ctx, "notification queue is full, the scan's changes are not sent", slog.Int64("scan", scanID))
	}
}

// Run sends each queued scan until ctx is done. It returns nil then.
func (n *Notifier) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case id := <-n.queue:
			n.sendScan(ctx, id)
		}
	}
}

// Destinations returns the configured destinations, in name order.
func (n *Notifier) Destinations() []*Destination {
	return n.destinations
}

// Destination returns the destination with the given name. It returns false
// when no destination has it.
func (n *Notifier) Destination(name string) (*Destination, bool) {
	for _, d := range n.destinations {
		if d.Name() == name {
			return d, true
		}
	}

	return nil, false
}

// Last returns the outcome of the last message to a destination. It returns
// false when the notifier sent none since the server started.
func (n *Notifier) Last(name string) (Result, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()

	r, ok := n.last[name]

	return r, ok
}

// Rules returns the kinds of change that the named destination gets, in name
// order.
func (n *Notifier) Rules(ctx context.Context, name string) ([]dbtype.EventKind, error) {
	kinds, err := n.q.NotifyRules(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("rules of %q: %w", name, err)
	}

	return kinds, nil
}

// SetRules replaces the kinds of change that the named destination gets. With
// no kinds, it gets nothing.
func (n *Notifier) SetRules(ctx context.Context, name string, kinds []dbtype.EventKind) error {
	tx, err := n.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin rules of %q: %w", name, err)
	}

	defer func() { _ = tx.Rollback() }()

	q := n.q.WithTx(tx)

	if err := q.DeleteNotifyRules(ctx, name); err != nil {
		return fmt.Errorf("clear rules of %q: %w", name, err)
	}

	for _, k := range kinds {
		if err := q.CreateNotifyRule(ctx, models.CreateNotifyRuleParams{Destination: name, Kind: k}); err != nil {
			return fmt.Errorf("add rule %s to %q: %w", k, name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit rules of %q: %w", name, err)
	}

	return nil
}

// SendTest sends d a test message. It records the outcome as the last result
// of d.
func (n *Notifier) SendTest(ctx context.Context, d *Destination) error {
	return n.send(ctx, d, Test())
}

// sendScan sends the changes of one scan to every destination that chose one
// of them. If a destination fails, sendScan logs it and still sends to the
// others.
func (n *Notifier) sendScan(ctx context.Context, scanID int64) {
	changes, err := n.store.ScanChanges(ctx, scanID)
	if err != nil {
		n.log.ErrorContext(ctx, "could not read the scan's changes to send", slog.Int64("scan", scanID), logger.Err(err))
		return
	}

	for _, d := range n.destinations {
		kinds, err := n.Rules(ctx, d.Name())
		if err != nil {
			n.log.ErrorContext(ctx, "could not read what a destination is sent",
				slog.String("destination", d.Name()), logger.Err(err))

			continue
		}

		m, ok := ForScan(scanID, changes, kinds)
		if !ok {
			continue
		}

		if err := n.send(ctx, d, m); err != nil {
			n.log.WarnContext(ctx, "could not send the scan's changes",
				slog.Int64("scan", scanID), slog.String("destination", d.Name()), logger.Err(err))
		}
	}
}

func (n *Notifier) send(ctx context.Context, d *Destination, m Message) error {
	err := d.Send(ctx, m)

	n.mu.Lock()
	n.last[d.Name()] = Result{At: time.Now(), Err: err}
	n.mu.Unlock()

	return err
}
