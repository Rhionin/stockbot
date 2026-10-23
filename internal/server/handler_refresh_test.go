package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"testing"

	"github.com/Rhionin/stockbot/internal/product"
)

// TestRefreshHandlerOutcomes covers Property 11: the endpoint refreshes
// regardless of freshness and reports the stored result. Refresh ignores the
// TTL entirely, so every case here calls upstream and every 200 response
// carries the row as it stands after the write, confirmed independently
// through exchanges(GET /api/products) rather than trusting the echo.
func TestRefreshHandlerOutcomes(t *testing.T) {
	tests := []handlerTestCase{
		{
			name: "upstream answer changed reports updated and persists the new values",
			setup: func(env testEnv) {
				setupExternalProduct("ext-updated", "Old Name", "Old Category", nil)(env)
				env.OpenFoodFacts.Seed("ext-updated", &product.ProductSummary{
					ID:            "ext-updated",
					Name:          "New Name",
					Category:      "New Category",
					UnitOfMeasure: "gallon",
					ImageURL:      "https://example.com/new.jpg",
				})
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/products/ext-updated/refresh",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.outcome", value: "updated"},
					{path: "$.product.name", value: "New Name"},
					{path: "$.product.category", value: "New Category"},
					{path: "$.product.unitOfMeasure", value: "gallon"},
					{path: "$.product.imageUrl", value: "https://example.com/new.jpg"},
				},
			},
			// Confirms the row was actually written, not just echoed back in the
			// response the handler assembled in memory.
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/products",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].name", value: "New Name"},
					{path: "$[0].category", value: "New Category"},
					{path: "$[0].unitOfMeasure", value: "gallon"},
					{path: "$[0].imageUrl", value: "https://example.com/new.jpg"},
				},
			}),
		},
		{
			name: "upstream answer identical to cache reports unchanged",
			setup: func(env testEnv) {
				setupExternalProduct("ext-unchanged", "Same Name", "Same Category", nil)(env)
				// UnitOfMeasure and ImageURL are left empty on both sides, so no
				// field differs and classify reports unchanged.
				env.OpenFoodFacts.Seed("ext-unchanged", &product.ProductSummary{
					ID:       "ext-unchanged",
					Name:     "Same Name",
					Category: "Same Category",
				})
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/products/ext-unchanged/refresh",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.outcome", value: "unchanged"},
					{path: "$.product.name", value: "Same Name"},
					{path: "$.product.category", value: "Same Category"},
				},
			},
		},
		{
			name: "fresh external row still calls upstream because Refresh ignores the TTL",
			setup: func(env testEnv) {
				now := env.Clock.Now()
				// refreshedAt is stamped as "now", well inside the 30-day test TTL,
				// so ScheduleRefresh would skip this row. The endpoint calls
				// Refresh directly and must not skip it.
				setupExternalProduct("ext-fresh", "Fresh Name", "Fresh Category", &now)(env)
				env.OpenFoodFacts.Seed("ext-fresh", &product.ProductSummary{
					ID:       "ext-fresh",
					Name:     "Fresh Name Updated",
					Category: "Fresh Category",
				})
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/products/ext-fresh/refresh",
				expectedStatus: http.StatusOK,
			},
			afterRequest: func(env testEnv) {
				if got := env.OpenFoodFacts.CallCount("ext-fresh"); got != 1 {
					env.T.Errorf("CallCount(ext-fresh) after refreshing a fresh row: want 1, got %d", got)
				}
			},
		},
		{
			name: "barcode absent from upstream reports not_found_upstream and preserves cached fields",
			setup: func(env testEnv) {
				prod := product.Product{
					ID:       "ext-notfound",
					Name:     "Cached Name",
					Category: "Cached Category",
					ImageURL: "https://example.com/cached.jpg",
					Source:   product.SourceExternal,
				}
				if err := env.ProductStore.CreateProduct(context.Background(), prod); err != nil {
					env.T.Fatalf("failed to create external product: %v", err)
				}
				// Deliberately not seeded in env.Upstream: LookupBarcode returns
				// product.ErrProductNotFound for any barcode it has never seen.
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/products/ext-notfound/refresh",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.outcome", value: "not_found_upstream"},
					{path: "$.product.name", value: "Cached Name"},
					{path: "$.product.imageUrl", value: "https://example.com/cached.jpg"},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/products",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].name", value: "Cached Name"},
					{path: "$[0].imageUrl", value: "https://example.com/cached.jpg"},
				},
			}),
		},
	}

	runHandlerTests(t, tests)
}

// TestRefreshHandlerFailuresAndOwnership covers the endpoint's failure and
// ownership paths:
//
//   - Property 13: Refresh never writes a row it does not own
//   - Property 14: The endpoint's failure statuses are total
//   - Property 17: Disabling external lookup makes refresh inert
func TestRefreshHandlerFailuresAndOwnership(t *testing.T) {
	tests := []handlerTestCase{
		{
			name: "upstream transport error reports 502 and preserves cached fields",
			setup: func(env testEnv) {
				prod := product.Product{
					ID:            "ext-transport-error",
					Name:          "Cached Name",
					Category:      "Cached Category",
					UnitOfMeasure: "each",
					ImageURL:      "https://example.com/cached.jpg",
					Source:        product.SourceExternal,
				}
				if err := env.ProductStore.CreateProduct(context.Background(), prod); err != nil {
					env.T.Fatalf("failed to create external product: %v", err)
				}
				// A transport-like error, distinct from product.ErrProductNotFound,
				// so Refresh takes the "any other error" branch rather than the
				// upstream-not-found branch.
				env.OpenFoodFacts.SeedError("ext-transport-error", errors.New("network timeout"))
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/products/ext-transport-error/refresh",
				expectedStatus: http.StatusBadGateway,
				assertions: []assertion{
					{path: "$.code", value: "provider_unavailable"},
					{path: "$.provider", value: "Open Food Facts"},
					{path: "$.error", value: "Open Food Facts isn't responding. Try again later."},
				},
				bodyExcludes: []string{"network timeout", "could not refresh"},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/products",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].name", value: "Cached Name"},
					{path: "$[0].category", value: "Cached Category"},
					{path: "$[0].unitOfMeasure", value: "each"},
					{path: "$[0].imageUrl", value: "https://example.com/cached.jpg"},
				},
			}),
		},
		{
			name: "unknown product id reports 404",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/products/does-not-exist/refresh",
				expectedStatus: http.StatusNotFound,
				assertions: []assertion{
					{path: "$.error", value: "product not found"},
				},
			},
		},
		{
			name:  "user-sourced row reports unchanged without calling upstream",
			setup: setupProduct("user-prod", "User Name", "User Category"),
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/products/user-prod/refresh",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.outcome", value: "unchanged"},
					{path: "$.product.name", value: "User Name"},
					{path: "$.product.category", value: "User Category"},
				},
			},
			afterRequest: func(env testEnv) {
				// Refresh short-circuits before ever consulting OpenFoodFacts for a
				// source = 'user' row, so no upstream request should have been made.
				if got := env.OpenFoodFacts.CallCount("user-prod"); got != 0 {
					env.T.Errorf("CallCount(user-prod): want 0, got %d", got)
				}
			},
		},
		{
			name: "external lookup disabled reports unchanged and leaves every column untouched",
			setup: func(env testEnv) {
				prod := product.Product{
					ID:            "ext-disabled",
					Name:          "Disabled Name",
					Category:      "Disabled Category",
					UnitOfMeasure: "each",
					ImageURL:      "https://example.com/disabled.jpg",
					Source:        product.SourceExternal,
					// RefreshedAt is nil, so the row starts stale. Disabling
					// external lookup must still leave it stale rather than
					// stamping it, or a later re-enable would treat it as fresh.
				}
				if err := env.ProductStore.CreateProduct(context.Background(), prod); err != nil {
					env.T.Fatalf("failed to create external product: %v", err)
				}
				// Seed an answer that would change every field if it were ever
				// consulted, so an accidental upstream call would be caught by the
				// unchanged-values assertions below rather than passing by luck.
				env.OpenFoodFacts.Seed("ext-disabled", &product.ProductSummary{
					ID:            "ext-disabled",
					Name:          "Should Not Apply",
					Category:      "Should Not Apply",
					UnitOfMeasure: "kg",
					ImageURL:      "https://example.com/should-not-apply.jpg",
				})
				env.Refresher.ExternalLookupEnabled = false
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/products/ext-disabled/refresh",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.outcome", value: "unchanged"},
					{path: "$.product.name", value: "Disabled Name"},
				},
			},
			afterRequest: func(env testEnv) {
				if got := env.OpenFoodFacts.CallCount("ext-disabled"); got != 0 {
					env.T.Errorf("CallCount(ext-disabled): want 0, got %d", got)
				}

				// refreshed_at is NULL here, and product.Product.RefreshedAt
				// carries omitempty, so a null refreshed_at renders as an absent
				// JSON field rather than a comparable value. A direct DB read is
				// the only way to confirm it, and it is the one column GET
				// /api/products cannot expose either way.
				var name, category, unitOfMeasure, imageURL, source string
				var refreshedAt sql.NullTime
				var nameOverridden bool
				row := env.DB.QueryRowContext(context.Background(),
					`SELECT name, COALESCE(category, ''), COALESCE(unit_of_measure, ''), COALESCE(image_url, ''),
					        source, refreshed_at, name_overridden
					 FROM products WHERE id = ?`, "ext-disabled")
				if err := row.Scan(&name, &category, &unitOfMeasure, &imageURL, &source, &refreshedAt, &nameOverridden); err != nil {
					env.T.Fatalf("query product: %v", err)
				}
				if name != "Disabled Name" {
					env.T.Errorf("name: want %q, got %q", "Disabled Name", name)
				}
				if category != "Disabled Category" {
					env.T.Errorf("category: want %q, got %q", "Disabled Category", category)
				}
				if unitOfMeasure != "each" {
					env.T.Errorf("unit_of_measure: want %q, got %q", "each", unitOfMeasure)
				}
				if imageURL != "https://example.com/disabled.jpg" {
					env.T.Errorf("image_url: want %q, got %q", "https://example.com/disabled.jpg", imageURL)
				}
				if source != product.SourceExternal {
					env.T.Errorf("source: want %q, got %q", product.SourceExternal, source)
				}
				if refreshedAt.Valid {
					env.T.Errorf("refreshed_at: want NULL, got %v", refreshedAt.Time)
				}
				if nameOverridden {
					env.T.Error("name_overridden: want false, got true")
				}
			},
		},
	}

	runHandlerTests(t, tests)
}

// TestRefreshHandlerNameOverrideFlow covers:
//
//   - Property 10: A name edit on an external row claims the name
//   - Property 7: A merge takes an upstream field only when upstream
//     supplied one
//
// Both cases PUT through the real UpdateHandler route rather than writing
// name_overridden directly, because it is UpdateProduct's CASE clause
// (source = 'external' AND name <> ?) that sets the flag, and only a real
// PUT exercises it.
func TestRefreshHandlerNameOverrideFlow(t *testing.T) {
	tests := []handlerTestCase{
		{
			name: "a name edit on an external row survives a refresh while category and image still update",
			setup: func(env testEnv) {
				setupExternalProduct("ext-name-override", "Cached Name", "Cached Category", nil)(env)

				// Submitted name differs from the stored name, so the CASE
				// clause sets name_overridden = true.
				exchanges(httpExchange{
					method:         "PUT",
					path:           "/api/products/ext-name-override",
					body:           `{"name":"My Custom Name","category":"Cached Category"}`,
					expectedStatus: http.StatusOK,
				})(env)

				// Seeded with a different name, category, and image so the
				// test can tell "the name survived" apart from "nothing
				// changed at all".
				env.OpenFoodFacts.Seed("ext-name-override", &product.ProductSummary{
					ID:       "ext-name-override",
					Name:     "Upstream Name",
					Category: "Upstream Category",
					ImageURL: "https://example.com/upstream.jpg",
				})
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/products/ext-name-override/refresh",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.outcome", value: "updated"},
					{path: "$.product.name", value: "My Custom Name"},
					{path: "$.product.category", value: "Upstream Category"},
					{path: "$.product.imageUrl", value: "https://example.com/upstream.jpg"},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/products",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].name", value: "My Custom Name"},
					{path: "$[0].category", value: "Upstream Category"},
					{path: "$[0].imageUrl", value: "https://example.com/upstream.jpg"},
				},
			}),
		},
		{
			name: "a category-only edit that submits the same name leaves the name unclaimed, so the next refresh still updates it",
			setup: func(env testEnv) {
				setupExternalProduct("ext-category-only", "Cached Name", "Cached Category", nil)(env)

				// Submitted name equals the stored name, so the CASE clause's
				// name <> ? comparison is false and name_overridden is left
				// at its prior value (false) rather than set — proving
				// Requirement 3.5 by observing the next refresh still
				// updates the name, not by asserting the column directly.
				exchanges(httpExchange{
					method:         "PUT",
					path:           "/api/products/ext-category-only",
					body:           `{"name":"Cached Name","category":"New Category"}`,
					expectedStatus: http.StatusOK,
				})(env)

				env.OpenFoodFacts.Seed("ext-category-only", &product.ProductSummary{
					ID:   "ext-category-only",
					Name: "Upstream Name Wins",
				})
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/products/ext-category-only/refresh",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.outcome", value: "updated"},
					{path: "$.product.name", value: "Upstream Name Wins"},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/products",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].name", value: "Upstream Name Wins"},
				},
			}),
		},
	}

	runHandlerTests(t, tests)
}
