package product

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Rhionin/stockbot/internal/tenant"
	"github.com/go-json-experiment/json"
	"github.com/google/uuid"
)

const contributionSettingKey = "contribute_products"

// Share statuses returned to the person who asked to contribute a product.
// not_configured, submitted, and failed are also stored. disabled and
// already_upstream are answers only: nothing was attempted.
const (
	ShareDisabled        = "disabled"
	ShareAlreadyUpstream = "already_upstream"
	ShareNotConfigured   = "not_configured"
	ShareSubmitted       = "submitted"
	ShareFailed          = "failed"
)

// ShareOutcome is the result of one explicit contribute request. It is omitted
// from the product response when the request did not ask to contribute.
type ShareOutcome struct {
	Status   string         `json:"status"`
	Database ExternalSource `json:"database,omitempty"`
	Barcode  string         `json:"barcode,omitempty"`
	Detail   string         `json:"detail,omitempty"`
}

// ShareRequest is one person's request to share a product they typed in.
// Contribute false means they did not ask, and RecordShare does nothing.
type ShareRequest struct {
	Contribute            bool
	ProductID             string
	Name                  string
	Category              string
	UnitOfMeasure         string
	Quantity              string
	Barcode               string
	Database              ExternalSource
	ProductSource         string
	ProductExternalSource ExternalSource
}

// Contribution is the payload a configured contributor may send upstream.
type Contribution struct {
	Barcode       string
	Name          string
	Category      string
	UnitOfMeasure string
	// Quantity is the net size, when it is known. It is what Open Food Facts
	// stores as quantity. The package word stays in UnitOfMeasure.
	Quantity string
	Database ExternalSource
}

// ContributionRecord is one stored share attempt.
type ContributionRecord struct {
	ID        string         `json:"id"`
	ProductID string         `json:"productId"`
	Barcode   string         `json:"barcode"`
	Database  ExternalSource `json:"database"`
	Status    string         `json:"status"`
	Detail    string         `json:"detail,omitempty"`
	CreatedAt time.Time      `json:"createdAt"`
}

// UpstreamContributor sends one opted-in product to a Product Opener database.
// Configured is false when this process has no account to send with, and
// Contribute must not be called in that case.
type UpstreamContributor interface {
	Configured() bool
	Contribute(ctx context.Context, contribution Contribution) error
}

// UnconfiguredContributor never contacts an upstream database. It is the
// default until PRODUCT_OPENER_USER_ID and PRODUCT_OPENER_PASSWORD are both
// set and external lookup is not disabled.
type UnconfiguredContributor struct{}

func (UnconfiguredContributor) Configured() bool { return false }

func (UnconfiguredContributor) Contribute(context.Context, Contribution) error {
	return errors.New("product sharing is not configured")
}

// ValidateContribution reports a problem with an explicit contribute request.
// A request that does not ask to contribute is always valid.
func ValidateContribution(contribute bool, barcode string, database ExternalSource) error {
	if !contribute {
		return nil
	}
	if database == "" || !database.Valid() {
		return errors.New("choose an open database to contribute this product to")
	}
	if barcode == "" {
		return errors.New("a barcode is required to contribute this product")
	}
	if !digitsOnly(barcode) || len(barcode) < 8 || len(barcode) > 14 {
		return errors.New("barcode must be 8 to 14 digits to contribute this product")
	}
	return nil
}

func digitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// DisplayName is the name a person sees for a Product Opener database.
func (s ExternalSource) DisplayName() string {
	switch s {
	case ExternalSourceOpenFoodFacts:
		return "Open Food Facts"
	case ExternalSourceOpenProductsFacts:
		return "Open Products Facts"
	case ExternalSourceOpenBeautyFacts:
		return "Open Beauty Facts"
	case ExternalSourceOpenPetFoodFacts:
		return "Open Pet Food Facts"
	default:
		return "the open database"
	}
}

// ContributionEnabled reports the household opt-in. No stored value means off.
func (r *Catalog) ContributionEnabled(ctx context.Context, scope tenant.Scope) (bool, error) {
	if err := scope.Require(); err != nil {
		return false, err
	}
	var value string
	err := r.db.QueryRowContext(ctx, `
		SELECT value FROM household_settings
		WHERE household_id = ? AND key = ?`,
		scope.HouseholdID(), contributionSettingKey).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		primary, perr := r.PrimaryScope(ctx)
		if perr != nil || primary.HouseholdID() != scope.HouseholdID() {
			return false, nil
		}
		err = r.db.QueryRowContext(ctx,
			`SELECT value FROM app_settings WHERE key = ?`, contributionSettingKey).Scan(&value)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
	}
	if err != nil {
		return false, fmt.Errorf("could not read contribution setting: %w", err)
	}
	return value == "true", nil
}

// SetContributionEnabled stores the household opt-in. false is the default
// and is still stored explicitly so a later read can tell a choice from absence.
func (r *Catalog) SetContributionEnabled(ctx context.Context, scope tenant.Scope, enabled bool) error {
	if err := scope.Require(); err != nil {
		return err
	}
	value := "false"
	if enabled {
		value = "true"
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO household_settings (household_id, key, value) VALUES (?, ?, ?)
		ON CONFLICT(household_id, key) DO UPDATE SET value = excluded.value`,
		scope.HouseholdID(), contributionSettingKey, value)
	if err != nil {
		return fmt.Errorf("could not save contribution setting: %w", err)
	}
	return nil
}

// InsertContribution stores one share attempt. An empty ID is filled in.
func (r *Catalog) InsertContribution(ctx context.Context, scope tenant.Scope, rec ContributionRecord) error {
	if err := scope.Require(); err != nil {
		return err
	}
	if rec.ID == "" {
		rec.ID = uuid.NewString()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO product_contributions (id, product_id, household_id, barcode, external_source, status, detail)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		rec.ID, rec.ProductID, scope.HouseholdID(), rec.Barcode, string(rec.Database), rec.Status, nullableString(rec.Detail))
	if err != nil {
		return fmt.Errorf("could not save contribution: %w", err)
	}
	return nil
}

// ListContributions returns share attempts, newest first. An empty productID
// lists every attempt.
func (r *Catalog) ListContributions(ctx context.Context, scope tenant.Scope, productID string) ([]ContributionRecord, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, product_id, barcode, external_source, status, COALESCE(detail, ''), created_at
		FROM product_contributions
		WHERE household_id = ? AND (? = '' OR product_id = ?)
		ORDER BY created_at DESC, id DESC`,
		scope.HouseholdID(), productID, productID)
	if err != nil {
		return nil, fmt.Errorf("could not list contributions: %w", err)
	}
	defer rows.Close()

	records := []ContributionRecord{}
	for rows.Next() {
		var rec ContributionRecord
		var database string
		if err := rows.Scan(&rec.ID, &rec.ProductID, &rec.Barcode, &database, &rec.Status, &rec.Detail, &rec.CreatedAt); err != nil {
			return nil, fmt.Errorf("could not list contributions: %w", err)
		}
		rec.Database = ExternalSource(database)
		records = append(records, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("could not list contributions: %w", err)
	}
	return records, nil
}

// BarcodesForProduct returns every barcode mapped to a product, sorted.
func (r *Catalog) BarcodesForProduct(ctx context.Context, scope tenant.Scope, productID string) ([]string, error) {
	if err := scope.Require(); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT barcode FROM barcodes
		WHERE product_id = ?
		  AND (
		        (source = 'user_override' AND household_id = ?)
		     OR (source = 'global' AND user_id = '')
		      )
		ORDER BY barcode`, productID, scope.HouseholdID())
	if err != nil {
		return nil, fmt.Errorf("could not list barcodes: %w", err)
	}
	defer rows.Close()

	barcodes := []string{}
	for rows.Next() {
		var barcode string
		if err := rows.Scan(&barcode); err != nil {
			return nil, fmt.Errorf("could not list barcodes: %w", err)
		}
		barcodes = append(barcodes, barcode)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("could not list barcodes: %w", err)
	}
	return barcodes, nil
}

// RecordShare applies the opt-in rules for one contribute request.
//
// Nothing is sent unless the request asked to contribute, the household
// setting is on, and the product was not already loaded from an upstream
// database. When this process has no Product Opener account, the attempt is
// saved locally as not_configured and no network call is made.
func RecordShare(ctx context.Context, scope tenant.Scope, catalog *Catalog, contributor UpstreamContributor, req ShareRequest) (*ShareOutcome, error) {
	if !req.Contribute {
		return nil, nil
	}
	if contributor == nil {
		contributor = UnconfiguredContributor{}
	}

	enabled, err := catalog.ContributionEnabled(ctx, scope)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return &ShareOutcome{
			Status: ShareDisabled,
			Detail: "Sharing is turned off, so this product stayed in your pantry.",
		}, nil
	}
	if req.ProductSource == SourceExternal || req.ProductExternalSource != "" {
		return &ShareOutcome{
			Status: ShareAlreadyUpstream,
			Detail: "This product already comes from an open database, so it was not sent.",
		}, nil
	}

	outcome := &ShareOutcome{
		Status:   ShareNotConfigured,
		Database: req.Database,
		Barcode:  req.Barcode,
		Detail:   "Saved in your pantry. Nothing was sent because this Stockbot is not signed in to the open databases.",
	}
	if contributor.Configured() {
		err := contributor.Contribute(ctx, Contribution{
			Barcode:       req.Barcode,
			Name:          req.Name,
			Category:      req.Category,
			UnitOfMeasure: req.UnitOfMeasure,
			Quantity:      req.Quantity,
			Database:      req.Database,
		})
		if err != nil {
			outcome.Status = ShareFailed
			outcome.Detail = err.Error()
		} else {
			outcome.Status = ShareSubmitted
			outcome.Detail = fmt.Sprintf("Shared with %s.", req.Database.DisplayName())
		}
	}

	if err := catalog.InsertContribution(ctx, scope, ContributionRecord{
		ProductID: req.ProductID,
		Barcode:   req.Barcode,
		Database:  req.Database,
		Status:    outcome.Status,
		Detail:    outcome.Detail,
	}); err != nil {
		return nil, err
	}
	return outcome, nil
}

const productOpenerUserAgent = "Stockbot (https://github.com/Rhionin/stockbot)"

// productOpenerWriteOrigins are the Product Opener site roots that accept
// POST /cgi/product_jqm2.pl. They are separate hosts from the read API, and
// each database has its own account system.
var productOpenerWriteOrigins = map[ExternalSource]string{
	ExternalSourceOpenFoodFacts:     "https://world.openfoodfacts.org",
	ExternalSourceOpenProductsFacts: "https://world.openproductsfacts.org",
	ExternalSourceOpenBeautyFacts:   "https://world.openbeautyfacts.org",
	ExternalSourceOpenPetFoodFacts:  "https://world.openpetfoodfacts.org",
}

// ProductOpenerContributor posts one opted-in product to a Product Opener
// write endpoint. It stays inert unless both account fields are set.
type ProductOpenerContributor struct {
	UserID     string
	Password   string
	HTTPClient *http.Client
	// Origins overrides write hosts. Tests point a single database at a fake
	// server; production leaves this nil and uses productOpenerWriteOrigins.
	Origins map[ExternalSource]string
}

// Configured reports whether this contributor has an account to send with.
func (c *ProductOpenerContributor) Configured() bool {
	return c != nil && c.UserID != "" && c.Password != ""
}

// NewContributorFromEnv builds the contributor the server should use.
// External lookup disabled, or either account variable unset, yields a
// contributor that never makes a network call. The password is not logged.
func NewContributorFromEnv(getenv func(string) string) UpstreamContributor {
	if getenv == nil || getenv("DISABLE_EXTERNAL_PRODUCT_LOOKUP") == "true" {
		return UnconfiguredContributor{}
	}
	userID := strings.TrimSpace(getenv("PRODUCT_OPENER_USER_ID"))
	password := getenv("PRODUCT_OPENER_PASSWORD")
	if userID == "" || password == "" {
		return UnconfiguredContributor{}
	}
	return &ProductOpenerContributor{
		UserID:   userID,
		Password: password,
		HTTPClient: &http.Client{
			Timeout: 15 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

type productOpenerWriteResponse struct {
	Status        int    `json:"status"`
	StatusVerbose string `json:"status_verbose"`
}

// Contribute posts the product to the selected database. Empty optional
// fields are omitted so a write cannot blank an upstream value we do not have.
func (c *ProductOpenerContributor) Contribute(ctx context.Context, contribution Contribution) error {
	if !c.Configured() {
		return errors.New("product sharing is not configured")
	}
	origin := c.origin(contribution.Database)
	if origin == "" {
		return errors.New("that open database is not available")
	}

	form := url.Values{}
	form.Set("user_id", c.UserID)
	form.Set("password", c.Password)
	form.Set("code", contribution.Barcode)
	form.Set("product_name", contribution.Name)
	form.Set("lc", "en")
	form.Set("comment", "Added with explicit opt-in from Stockbot (https://github.com/Rhionin/stockbot)")
	if contribution.Category != "" {
		form.Set("categories", contribution.Category)
	}
	quantity := contribution.Quantity
	if quantity == "" {
		quantity = contribution.UnitOfMeasure
	}
	if quantity != "" {
		form.Set("quantity", quantity)
	}

	endpoint := strings.TrimRight(origin, "/") + "/cgi/product_jqm2.pl"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return errors.New("could not prepare the product to share")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", productOpenerUserAgent)

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return errors.New("could not reach the open database")
	}
	defer res.Body.Close()

	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return errors.New("the open database returned an unreadable response")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return errors.New("the open database did not accept this product")
	}

	var parsed productOpenerWriteResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return errors.New("the open database returned an unreadable response")
	}
	if parsed.Status != 1 {
		return errors.New(safeWriteDetail(parsed.StatusVerbose, c.Password))
	}
	return nil
}

func (c *ProductOpenerContributor) origin(database ExternalSource) string {
	if c.Origins != nil {
		return c.Origins[database]
	}
	return productOpenerWriteOrigins[database]
}

func safeWriteDetail(verbose, password string) string {
	verbose = strings.TrimSpace(verbose)
	if verbose == "" || (password != "" && strings.Contains(verbose, password)) {
		return "the open database did not accept this product"
	}
	return verbose
}
