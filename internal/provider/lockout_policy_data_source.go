package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"

	"github.com/AbSciBio/terraform-provider-weka/internal/wekaapi"
)

var (
	_ datasource.DataSource              = (*lockoutPolicyDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*lockoutPolicyDataSource)(nil)
)

func NewLockoutPolicyDataSource() datasource.DataSource {
	return &lockoutPolicyDataSource{}
}

type lockoutPolicyDataSource struct {
	api *wekaapi.API
}

func (d *lockoutPolicyDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_lockout_policy"
}

func (d *lockoutPolicyDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The cluster account lockout policy. There is one per cluster.",
		Attributes: map[string]schema.Attribute{
			"id":                schema.StringAttribute{Description: "Always `cluster`.", Computed: true},
			"lockout_duration":  schema.Int64Attribute{Description: "Seconds an account stays locked after too many failed sign-ins.", Computed: true},
			"num_failed_logins": schema.Int64Attribute{Description: "Failed sign-ins before an account is locked.", Computed: true},
		},
	}
}

func (d *lockoutPolicyDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.api = configuredAPI(req.ProviderData, &resp.Diagnostics)
}

func (d *lockoutPolicyDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	// Reuses lockoutPolicyResource.get, which already handles the response shape.
	m, err := (&lockoutPolicyResource{api: d.api}).get(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading lockout policy", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}
