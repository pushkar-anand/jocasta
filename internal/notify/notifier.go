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
// minutes apart, so the queue fills only while every send is timing out; a
// scan that finds it full is logged and not sent.
const queueSize = 64

// Notifier sends each finished scan's changes to the destinations that asked
// for them. Scans are handed to it with Queue and sent by Run, on its own
// goroutine, so a slow or unreachable service never holds up a scan.
//
// Scans still waiting when the server stops are not sent.
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

// Result is how the last message to one destination went.
type Result struct {
	At  time.Time
	Err error // nil when it was delivered
}

// New returns a notifier that reads each scan's changes from store, and keeps
// each destination's chosen kinds in conn.
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

// Queue hands a finished scan to Run. It never blocks.
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

// Destination returns the destination with the given name, and false when
// none has it.
func (n *Notifier) Destination(name string) (*Destination, bool) {
	for _, d := range n.destinations {
		if d.Name() == name {
			return d, true
		}
	}

	return nil, false
}

// Last returns how the last message to a destination went, and false when
// none has been sent since the server started.
func (n *Notifier) Last(name string) (Result, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()

	r, ok := n.last[name]

	return r, ok
}

// Rules returns the kinds of change the named destination is sent, in name
// order.
func (n *Notifier) Rules(ctx context.Context, name string) ([]dbtype.EventKind, error) {
	kinds, err := n.q.NotifyRules(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("rules of %q: %w", name, err)
	}

	return kinds, nil
}

// SetRules replaces the kinds of change the named destination is sent. No
// kinds sends it nothing.
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

// SendTest sends d a test message, recorded as its last result.
func (n *Notifier) SendTest(ctx context.Context, d *Destination) error {
	return n.send(ctx, d, Test())
}

// sendScan sends one scan's changes to every destination that chose any of
// them. A destination that fails is logged and the others are still sent.
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
