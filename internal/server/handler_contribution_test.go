package server

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Rhionin/stockbot/internal/product"
)

// recordingContributor stands in for the Product Opener writer. It records
// calls and never touches the network.
type recordingContributor struct {
	configured bool
	calls      []product.Contribution
	err        error
}

func (r *recordingContributor) Configured() bool { return r.configured }

func (r *recordingContributor) Contribute(_ context.Context, contribution product.Contribution) error {
	r.calls = append(r.calls, contribution)
	return r.err
}

func enableContribution(env testEnv) {
	scope, err := env.ProductStore.PrimaryScope(context.Background())
	if err != nil {
		env.T.Fatalf("primary household: %v", err)
	}
	if err := env.ProductStore.SetContributionEnabled(context.Background(), scope, true); err != nil {
		env.T.Fatalf("enable contribution: %v", err)
	}
}

func TestContributionSettings(t *testing.T) {
	tests := []handlerTestCase{
		{
			name: "sharing is off and unsigned-in by default",
			httpExchange: httpExchange{
				method:         "GET",
				path:           "/api/settings/contribution",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.enabled", value: false},
					{path: "$.configured", value: false},
				},
			},
		},
		{
			name: "turning sharing on does not sign the server in",
			httpExchange: httpExchange{
				method:         "PUT",
				path:           "/api/settings/contribution",
				body:           `{"enabled":true}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.enabled", value: true},
					{path: "$.configured", value: false},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/settings/contribution",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.enabled", value: true},
					{path: "$.configured", value: false},
				},
			}),
		},
		{
			name: "sharing can be turned back off",
			setup: func(env testEnv) {
				enableContribution(env)
			},
			httpExchange: httpExchange{
				method:         "PUT",
				path:           "/api/settings/contribution",
				body:           `{"enabled":false}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.enabled", value: false},
				},
			},
		},
	}
	runHandlerTests(t, tests)
}

func TestContributionLocalPath(t *testing.T) {
	const barcode = "012345678905"
	tests := []handlerTestCase{
		{
			name: "creating a product does not contribute unless asked",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/products",
				body:           `{"name":"Orange","category":"Fruit","unitOfMeasure":"each"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.name", value: "Orange"},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/contributions",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$", value: []interface{}{}},
				},
			}),
		},
		{
			name: "an explicit contribute request is ignored while sharing is off",
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/products",
				body:           `{"name":"Orange","category":"Fruit","unitOfMeasure":"each","contribute":true,"contributeTo":"openfoodfacts","barcode":"` + barcode + `"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.name", value: "Orange"},
					{path: "$.contribution.status", value: "disabled"},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/contributions",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$", value: []interface{}{}},
				},
			}),
		},
		{
			name: "contribute without a barcode is rejected and saves nothing",
			setup: func(env testEnv) {
				enableContribution(env)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/products",
				body:           `{"name":"Orange","contribute":true,"contributeTo":"openfoodfacts"}`,
				expectedStatus: http.StatusBadRequest,
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/products",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$", value: []interface{}{}},
				},
			}),
		},
		{
			name: "contribute without a database is rejected",
			setup: func(env testEnv) {
				enableContribution(env)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/products",
				body:           `{"name":"Orange","contribute":true,"barcode":"` + barcode + `"}`,
				expectedStatus: http.StatusBadRequest,
			},
		},
		{
			name: "opted-in product is saved locally when the server is not signed in",
			setup: func(env testEnv) {
				enableContribution(env)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/products",
				body:           `{"name":"House Sponge","category":"Cleaning","unitOfMeasure":"each","contribute":true,"contributeTo":"openproductsfacts","barcode":"` + barcode + `"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.name", value: "House Sponge"},
					{path: "$.contribution.status", value: "not_configured"},
					{path: "$.contribution.database", value: "openproductsfacts"},
					{path: "$.contribution.barcode", value: barcode},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/contributions",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].status", value: "not_configured"},
					{path: "$[0].database", value: "openproductsfacts"},
					{path: "$[0].barcode", value: barcode},
				},
			}),
		},
		{
			name: "editing a typed-in product records a local contribution",
			setup: func(env testEnv) {
				enableContribution(env)
				setupProduct("prod-local", "Old Sponge", "Cleaning")(env)
			},
			httpExchange: httpExchange{
				method:         "PUT",
				path:           "/api/products/prod-local",
				body:           `{"name":"House Sponge","category":"Cleaning","unitOfMeasure":"each","contribute":true,"contributeTo":"openproductsfacts","barcode":"` + barcode + `"}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.name", value: "House Sponge"},
					{path: "$.contribution.status", value: "not_configured"},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/products/prod-local/contributions",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$[0].status", value: "not_configured"},
					{path: "$[0].barcode", value: barcode},
				},
			}),
		},
		{
			name: "a product that already came from upstream is not sent back",
			setup: func(env testEnv) {
				enableContribution(env)
				setupExternalProduct("prod-ext", "Known Cereal", "Breakfast", nil)(env)
			},
			httpExchange: httpExchange{
				method:         "PUT",
				path:           "/api/products/prod-ext",
				body:           `{"name":"Known Cereal","category":"Breakfast","unitOfMeasure":"box","contribute":true,"contributeTo":"openfoodfacts","barcode":"` + barcode + `"}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.contribution.status", value: "already_upstream"},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/contributions",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$", value: []interface{}{}},
				},
			}),
		},
	}

	runHandlerTests(t, tests)
}

func TestContributionCallsUpstreamOnlyWhenOptedInAndSignedIn(t *testing.T) {
	const barcode = "012345678905"
	reject := errors.New("the open database did not accept this product")

	tests := []handlerTestCase{
		{
			name:        "sharing off never calls upstream",
			contributor: &recordingContributor{configured: true},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/products",
				body:           `{"name":"Orange","contribute":true,"contributeTo":"openfoodfacts","barcode":"` + barcode + `"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.contribution.status", value: "disabled"},
				},
			},
			afterRequest: func(env testEnv) {
				assertContributionCalls(env, 0)
			},
		},
		{
			name:        "the per-product checkbox off never calls upstream",
			contributor: &recordingContributor{configured: true},
			setup: func(env testEnv) {
				enableContribution(env)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/products",
				body:           `{"name":"Orange","contribute":false,"contributeTo":"openfoodfacts","barcode":"` + barcode + `"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.name", value: "Orange"},
				},
			},
			afterRequest: func(env testEnv) {
				assertContributionCalls(env, 0)
			},
		},
		{
			name:        "both opt-ins send exactly one contribution",
			contributor: &recordingContributor{configured: true},
			setup: func(env testEnv) {
				enableContribution(env)
			},
			httpExchange: httpExchange{
				method:         "POST",
				path:           "/api/products",
				body:           `{"name":"House Sponge","category":"Cleaning","unitOfMeasure":"each","contribute":true,"contributeTo":"openproductsfacts","barcode":"` + barcode + `"}`,
				expectedStatus: http.StatusCreated,
				assertions: []assertion{
					{path: "$.contribution.status", value: "submitted"},
					{path: "$.contribution.database", value: "openproductsfacts"},
				},
			},
			afterRequest: func(env testEnv) {
				calls := assertContributionCalls(env, 1)
				call := calls[0]
				if call.Barcode != barcode || call.Name != "House Sponge" || call.Database != product.ExternalSourceOpenProductsFacts {
					env.T.Fatalf("contribution = %+v", call)
				}
			},
		},
		{
			name:        "an upstream rejection still keeps the product",
			contributor: &recordingContributor{configured: true, err: reject},
			setup: func(env testEnv) {
				enableContribution(env)
				setupProduct("prod-local", "Old Sponge", "Cleaning")(env)
			},
			httpExchange: httpExchange{
				method:         "PUT",
				path:           "/api/products/prod-local",
				body:           `{"name":"House Sponge","category":"Cleaning","contribute":true,"contributeTo":"openbeautyfacts","barcode":"` + barcode + `"}`,
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.name", value: "House Sponge"},
					{path: "$.contribution.status", value: "failed"},
					{path: "$.contribution.detail", value: reject.Error()},
				},
			},
			afterRequest: exchanges(httpExchange{
				method:         "GET",
				path:           "/api/products/prod-local",
				expectedStatus: http.StatusOK,
				assertions: []assertion{
					{path: "$.name", value: "House Sponge"},
				},
			}),
		},
	}
	runHandlerTests(t, tests)
}

func assertContributionCalls(env testEnv, want int) []product.Contribution {
	env.T.Helper()
	recorder, ok := env.Contributor.(*recordingContributor)
	if !ok {
		env.T.Fatalf("contributor type = %T", env.Contributor)
	}
	if len(recorder.calls) != want {
		env.T.Fatalf("upstream calls = %d, want %d", len(recorder.calls), want)
	}
	return recorder.calls
}
