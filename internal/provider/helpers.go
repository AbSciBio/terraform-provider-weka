package provider

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"

	"gitlab.com/absci/devops/terraform-provider-weka/internal/wekaapi"
)

// configuredAPI unpacks ProviderData in a resource or data source Configure.
// It returns nil before the provider is configured (validation runs that early).
func configuredAPI(data any, diags *diag.Diagnostics) *wekaapi.API {
	if data == nil {
		return nil
	}
	api, ok := data.(*wekaapi.API)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("expected *wekaapi.API, got %T", data))
	}
	return api
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}
