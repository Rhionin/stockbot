package cart

import (
	"context"

	"github.com/Rhionin/stockbot/internal/tenant"
)

// Provisioner is the seam that replaces CartExporter.
// It performs provisioning against a specific provider and returns a detailed report.
type Provisioner interface {
	Provision(ctx context.Context, scope tenant.Scope, provider ProviderID) (ProvisionReport, error)
}

// NoOpProvisioner is the no-op Provisioner used when no provider is configured.
type NoOpProvisioner struct{}

// Provision returns a no-op report with no confirmed entries.
func (n *NoOpProvisioner) Provision(_ context.Context, _ tenant.Scope, _ ProviderID) (ProvisionReport, error) {
	return ProvisionReport{
		Confirmed: 0,
		Entries:   nil,
	}, nil
}

// ProvisionConflict is a refusal that sends nothing and changes nothing.
// The HTTP layer maps it to 409. Reason is the message returned to the client.
type ProvisionConflict struct {
	Reason string
}

func (e *ProvisionConflict) Error() string { return e.Reason }
