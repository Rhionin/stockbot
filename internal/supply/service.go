package supply

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	"github.com/Rhionin/stockbot/internal/restock"
	"github.com/Rhionin/stockbot/internal/shopping"
	"github.com/Rhionin/stockbot/internal/tenant"
)

// Months is a supply window. A month is 30 days. Zero is invalid.
// Days is the only conversion. Rate math, the replace-only window, and
// the copy sentence all use it. Do not call AddDate.
type Months int

// DefaultMonths is the account window when the household default has not been set.
const DefaultMonths Months = 3

// WipePhrase is the sole copy of the typed confirmation.
const WipePhrase = "WIPE INVENTORY"

// ParseMonths accepts 1 through 12 inclusive.
func ParseMonths(n int) (Months, error) {
	if n < 1 || n > 12 {
		return 0, fmt.Errorf("supply length must be from 1 to 12 months")
	}
	return Months(n), nil
}

// Days returns the window as 30-day months.
func (m Months) Days() int { return int(m) * 30 }

// Units is a count of sellable units. Zero is valid on a line (not emitted)
// and invalid as an override.
type Units int

// ParseUnits accepts 1 through 999 inclusive.
func ParseUnits(n int) (Units, error) {
	if n < 1 || n > 999 {
		return 0, fmt.Errorf("supply quantity must be from 1 to 999")
	}
	return Units(n), nil
}

// ProductID identifies a catalog product.
type ProductID string

// GroupID is a product group. Empty means the product is planned on its own.
type GroupID string

// Phase is the onboarding state machine.
// The zero value is Opening: no start instant is stored.
// A non-zero instant is Using since that instant.
// Replace-only is not stored. It is now < started+30d, inside derive.
type Phase struct{ started time.Time }

func (p Phase) isOpening() bool { return p.started.IsZero() }

// StartedAt reports the snapshot completion instant.
// The bool is false while opening. It does not add 30 days.
func (p Phase) StartedAt() (time.Time, bool) {
	if p.started.IsZero() {
		return time.Time{}, false
	}
	return p.started, true
}

type overrideKind uint8

const (
	overrideInherit overrideKind = iota
	overrideWindow
	overrideQuantity
)

// Override is the product exception. The zero value inherits account Months.
// Constructors set exactly one arm. Fields are unexported, so a composite
// literal in another package cannot set both a window and a quantity.
type Override struct {
	kind   overrideKind
	months Months
	units  Units
}

// Window is a product supply window. It replaces the account window once a rate qualifies.
func Window(m Months) (Override, error) {
	parsed, err := ParseMonths(int(m))
	if err != nil {
		return Override{}, err
	}
	return Override{kind: overrideWindow, months: parsed}, nil
}

// Quantity is a fixed par. It applies as soon as opening is over.
func Quantity(u Units) (Override, error) {
	parsed, err := ParseUnits(int(u))
	if err != nil {
		return Override{}, err
	}
	return Override{kind: overrideQuantity, units: parsed}, nil
}

// Window reports the product window when that arm is set.
func (o Override) Window() (Months, bool) {
	if o.kind != overrideWindow {
		return 0, false
	}
	return o.months, true
}

// Quantity reports the fixed par when that arm is set.
func (o Override) Quantity() (Units, bool) {
	if o.kind != overrideQuantity {
		return 0, false
	}
	return o.units, true
}

// Withdrawal is one consumption moment after merge of same-timestamp events.
// Qty comes from consumption rows. Each row is one unit. Stock-ins are not withdrawals.
type Withdrawal struct {
	At  time.Time
	Qty Units
}

// Fact is one catalog product, already summed across its items.
// StockIns and StockOuts contain only instants strictly after the start.
// Opening scans are neither. The provider-ledger timestamp is not an input.
// Manual nil means no pinned row. Group is set only when the product belongs
// to a product group. A grouped product's supply override is ignored.
type Fact struct {
	Product        ProductID
	Group          GroupID
	Name           string
	OnHand         Units
	Override       Override
	Manual         *Units
	StockIns       []time.Time
	StockOuts      []Withdrawal
	ItemID         string
	NetBase        *float64
	NetDimension   string
	LastConsumed   time.Time
	LastStocked    time.Time
	Rule           string
	Pinned         string
	NoRestock      bool
	GroupWindow    int
	GroupHasQty    bool
	GroupBase      float64
	GroupDimension string
	Deal           *shopping.Deal
	// Resolved is set by the loader. The zero Level keeps the older plan path
	// for tests that build facts by hand.
	Resolved restock.Resolution
}

// LineSource names why a shopping line exists.
type LineSource uint8

const (
	SourceManual LineSource = iota + 1
	SourceReplace
	SourceRate
	SourceFixed
)

// Line is one shopping-list row.
// Buy is the quantity to purchase, never a par.
// For SourceRate and SourceFixed, Buy = max(0, par - groupOnHand) and the
// note's "so N for M months" uses par, not Buy.
// Product on a collapsed line is the member that supplied the winning par,
// tie broken by smaller ProductID. Replace ties break by greater unreplaced
// quantity, then smaller ProductID.
// GroupID is set when the line was planned for a product group. Note then
// says why that member was picked.
type Line struct {
	Product ProductID
	Buy     Units
	Note    string
	Source  LineSource
	GroupID string
}

// Settings is the read model for the banner and the settings page.
// Opening is the absence of a start instant. StartedAt is zero while opening.
// Replace-only is intentionally absent.
type Settings struct {
	Months     Months
	Opening    bool
	StartedAt  time.Time
	WipePhrase string
}

// Service is the only public API. plan, derive, and SQL are unexported
// in this package, so a handler cannot import them.
type Service struct{ db *sql.DB }

// Open is the only exported constructor.
func Open(db *sql.DB) *Service {
	return &Service{db: db}
}

// GroupUsage is the amount rate for one product group, or nil when usage
// is not known yet. It writes nothing.
func (s *Service) GroupUsage(ctx context.Context, scope tenant.Scope, groupID string, now time.Time) (*Usage, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	if groupID == "" {
		return nil, nil
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("could not read supply: %w", err)
	}
	defer tx.Rollback()

	phase, _, err := readPhaseAndMonths(ctx, tx)
	if err != nil {
		return nil, err
	}
	facts, err := loadFacts(ctx, scope, tx, phase)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("could not read supply: %w", err)
	}
	var members []Fact
	for _, fact := range facts {
		if string(fact.Group) == groupID {
			members = append(members, fact)
		}
	}
	return usageFromMembers(now, phase, members), nil
}

// Plan loads phase, months, and facts in one read transaction, then calls plan.
// It writes nothing.
func (s *Service) Plan(ctx context.Context, scope tenant.Scope, now time.Time) ([]Line, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("could not read supply: %w", err)
	}
	defer tx.Rollback()

	phase, months, err := readPhaseAndMonths(ctx, tx)
	if err != nil {
		return nil, err
	}
	facts, err := loadFacts(ctx, scope, tx, phase)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("could not read supply: %w", err)
	}
	return plan(now, phase, months, facts), nil
}

// Complete inserts onboarding_started_at only when the key is absent, then re-reads.
// The stored instant wins. A second call does not move it. now is stored in UTC.
func (s *Service) Complete(ctx context.Context, scope tenant.Scope, now time.Time) (Phase, error) {
	if err := scope.Require(); err != nil {
		return Phase{}, err
	}
	return completeOpening(ctx, s.db, now.UTC())
}

// Settings reads the banner and settings-page model.
func (s *Service) Settings(ctx context.Context, scope tenant.Scope) (Settings, error) {
	if err := scope.Require(); err != nil {
		return Settings{}, err
	}
	return readSettings(ctx, s.db)
}

// OpeningTx reports whether the household is still taking the opening snapshot.
// The scan commit already holds tx. A second transaction cannot share that
// connection: the process keeps a single SQLite connection, and an in-memory
// test database is a different database on every extra connection.
func (s *Service) OpeningTx(ctx context.Context, scope tenant.Scope, tx *sql.Tx) (bool, error) {
	if err := scope.Require(); err != nil {
		return false, err
	}
	phase, err := readPhase(ctx, tx)
	if err != nil {
		return false, err
	}
	return phase.isOpening(), nil
}

// SetMonths stores the account window. Callers reject zero with ParseMonths first.
func (s *Service) SetMonths(ctx context.Context, scope tenant.Scope, m Months) error {
	if err := scope.Require(); err != nil {
		return err
	}
	if _, err := ParseMonths(int(m)); err != nil {
		return err
	}
	return writeMonths(ctx, s.db, m)
}

// SetOverride with a zero Override deletes the product's row.
// Deleting a missing row is success. A non-zero override upserts.
func (s *Service) SetOverride(ctx context.Context, scope tenant.Scope, id ProductID, o Override) error {
	return writeOverride(ctx, scope, s.db, id, o)
}

// Override reads the product exception. The zero value means inherit the account window.
func (s *Service) Override(ctx context.Context, scope tenant.Scope, id ProductID) (Override, error) {
	return readOverride(ctx, scope, s.db, id)
}

// Wipe returns an error without opening a transaction when phrase != WipePhrase.
// Otherwise one transaction clears that household's stock and the start date.
// The item and every restock target stay. Deleting the item row is what removes its target.
func (s *Service) Wipe(ctx context.Context, scope tenant.Scope, phrase string) error {
	if err := scope.Require(); err != nil {
		return err
	}
	if phrase != WipePhrase {
		return fmt.Errorf("Type WIPE INVENTORY to confirm wiping the inventory.")
	}
	return wipeHousehold(ctx, scope, s.db)
}

func plan(now time.Time, phase Phase, account Months, facts []Fact) []Line {
	for _, fact := range facts {
		if fact.Resolved.Level != "" {
			return planResolved(now, phase, account, facts)
		}
	}
	return planLegacy(now, phase, account, facts)
}

func planResolved(now time.Time, phase Phase, account Months, facts []Fact) []Line {
	lines := make([]Line, 0)
	pools := map[string][]Fact{}
	var solos []Fact
	for _, fact := range facts {
		if fact.Manual != nil {
			if *fact.Manual > 0 {
				lines = append(lines, Line{
					Product: fact.Product,
					Buy:     *fact.Manual,
					Source:  SourceManual,
				})
			}
			continue
		}
		if fact.Group != "" {
			pools[string(fact.Group)] = append(pools[string(fact.Group)], fact)
			continue
		}
		if fact.Resolved.Target.Value == 0 {
			continue
		}
		solos = append(solos, withResolvedOverride(fact))
	}
	if !phase.isOpening() {
		for _, members := range pools {
			pooled, alone := splitResolvedGroup(members)
			solos = append(solos, alone...)
			if len(pooled) == 0 || groupTargetIsZero(pooled) {
				continue
			}
			if line, ok := planProductGroup(now, phase, account, pooled); ok {
				lines = append(lines, line)
			}
		}
		for _, fact := range solos {
			if line, ok := planGroup(now, phase, account, []Fact{fact}); ok {
				lines = append(lines, line)
			}
		}
	} else {
		// Wipe clears the start date, so the household is opening again, but a
		// saved item amount has to shop as soon as that product is stocked in.
		lines = append(lines, openingAmountLines(now, phase, account, pools, solos)...)
	}
	sort.Slice(lines, func(i, j int) bool {
		return lines[i].Product < lines[j].Product
	})
	return lines
}

// splitResolvedGroup keeps a group together at the household window when the
// group itself sets nothing. An item target above 0 is planned on its own.
// An item target of 0 stays in the pool so its stock still counts, and it is
// not the product the group buys.
func openingAmountLines(now time.Time, phase Phase, account Months, pools map[string][]Fact, solos []Fact) []Line {
	var lines []Line
	planAmount := func(fact Fact) {
		if fact.Resolved.Level != restock.LevelItem || fact.Resolved.Target.Kind != restock.KindAmount || fact.Resolved.Target.Value <= 0 {
			return
		}
		if line, ok := planGroup(now, phase, account, []Fact{fact}); ok {
			lines = append(lines, line)
		}
	}
	for _, members := range pools {
		_, alone := splitResolvedGroup(members)
		for _, fact := range alone {
			planAmount(fact)
		}
	}
	for _, fact := range solos {
		planAmount(fact)
	}
	return lines
}

func splitResolvedGroup(members []Fact) (pooled []Fact, solos []Fact) {
	for _, member := range members {
		if member.Resolved.Level == restock.LevelItem && member.Resolved.Target.Value > 0 {
			solos = append(solos, withResolvedOverride(member))
			continue
		}
		if member.Resolved.Level == restock.LevelItem && member.Resolved.Target.Value == 0 {
			member.NoRestock = true
		}
		pooled = append(pooled, member)
	}
	return pooled, solos
}

func groupTargetIsZero(members []Fact) bool {
	for _, member := range members {
		if member.Resolved.Level == restock.LevelGroup && member.Resolved.Target.Value == 0 {
			return true
		}
	}
	return false
}

func withResolvedOverride(fact Fact) Fact {
	target := fact.Resolved.Target
	switch {
	case target.Kind == restock.KindMonths && target.Value > 0:
		if override, err := Window(Months(roundHalfUp(target.Value))); err == nil {
			fact.Override = override
		}
	case target.Kind == restock.KindAmount && target.Value > 0:
		if override, err := Quantity(Units(roundHalfUp(target.Value))); err == nil {
			fact.Override = override
		}
	}
	return fact
}

func planLegacy(now time.Time, phase Phase, account Months, facts []Fact) []Line {
	lines := make([]Line, 0)
	groups := map[string][]Fact{}
	for _, fact := range facts {
		if fact.Manual != nil {
			if *fact.Manual > 0 {
				lines = append(lines, Line{
					Product: fact.Product,
					Buy:     *fact.Manual,
					Source:  SourceManual,
				})
			}
			continue
		}
		key := string(fact.Group)
		if key == "" {
			key = "\x00" + string(fact.Product)
		}
		groups[key] = append(groups[key], fact)
	}
	if !phase.isOpening() {
		for _, members := range groups {
			var line Line
			var ok bool
			if members[0].Group != "" {
				line, ok = planProductGroup(now, phase, account, members)
			} else {
				line, ok = planGroup(now, phase, account, members)
			}
			if ok {
				lines = append(lines, line)
			}
		}
	}
	sort.Slice(lines, func(i, j int) bool {
		return lines[i].Product < lines[j].Product
	})
	return lines
}

type classified struct {
	fact       Fact
	hasPar     bool
	par        Units
	source     LineSource
	note       string
	unreplaced Units
}

func planGroup(now time.Time, phase Phase, account Months, members []Fact) (Line, bool) {
	started, _ := phase.StartedAt()
	classifiedMembers := make([]classified, 0, len(members))
	anyPar := false
	for _, member := range members {
		c := classified{fact: member}
		if qty, ok := member.Override.Quantity(); ok {
			c.hasPar = true
			c.par = qty
			c.source = SourceFixed
			anyPar = true
			classifiedMembers = append(classifiedMembers, c)
			continue
		}
		perDay, ok := derive(now, started, member.StockIns, member.StockOuts)
		if ok {
			window := account
			if w, hasWindow := member.Override.Window(); hasWindow {
				window = w
			}
			par := Units(roundHalfUp(perDay * float64(window.Days())))
			perMonth := roundHalfUp(perDay * 30)
			c.hasPar = true
			c.par = par
			c.source = SourceRate
			c.note = noteRate(perMonth, int(par), window)
			anyPar = true
			classifiedMembers = append(classifiedMembers, c)
			continue
		}
		c.unreplaced = unreplacedQty(started, member)
		c.source = SourceReplace
		classifiedMembers = append(classifiedMembers, c)
	}

	if anyPar {
		return parLine(classifiedMembers)
	}
	return replaceLine(classifiedMembers)
}

func parLine(members []classified) (Line, bool) {
	var winner *classified
	var onHand Units
	for i := range members {
		member := &members[i]
		onHand += member.fact.OnHand
		if !member.hasPar {
			continue
		}
		if winner == nil || member.par > winner.par || (member.par == winner.par && member.fact.Product < winner.fact.Product) {
			winner = member
		}
	}
	if winner == nil {
		return Line{}, false
	}
	buy := winner.par - onHand
	if buy < 0 {
		buy = 0
	}
	if buy == 0 {
		return Line{}, false
	}
	note := ""
	if winner.source == SourceRate {
		note = winner.note
	}
	return Line{
		Product: winner.fact.Product,
		Buy:     buy,
		Note:    note,
		Source:  winner.source,
	}, true
}

func replaceLine(members []classified) (Line, bool) {
	var total Units
	var winner *classified
	for i := range members {
		member := &members[i]
		total += member.unreplaced
		if winner == nil || member.unreplaced > winner.unreplaced || (member.unreplaced == winner.unreplaced && member.fact.Product < winner.fact.Product) {
			winner = member
		}
	}
	if winner == nil || total == 0 {
		return Line{}, false
	}
	return Line{
		Product: winner.fact.Product,
		Buy:     total,
		Note:    noteReplace(total),
		Source:  SourceReplace,
	}, true
}

// unreplaced counts withdrawals strictly after the later of the start instant
// and the latest stock-in. Withdrawals before the first stock-in still count
// when no stock-in has moved that boundary.
func unreplacedQty(started time.Time, fact Fact) Units {
	boundary := started
	for _, in := range fact.StockIns {
		if in.After(boundary) {
			boundary = in
		}
	}
	var sum Units
	for _, out := range fact.StockOuts {
		if out.At.After(boundary) && out.Qty > 0 {
			sum += out.Qty
		}
	}
	return sum
}
