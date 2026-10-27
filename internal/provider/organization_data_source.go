package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"

	"github.com/AbSciBio/terraform-provider-weka/internal/wekaapi"
)

var (
	_ datasource.DataSource                     = (*organizationDataSource)(nil)
	_ datasource.DataSourceWithConfigure        = (*organizationDataSource)(nil)
	_ datasource.DataSourceWithConfigValidators = (*organizationDataSource)(nil)
)

func NewOrganizationDataSource() datasource.DataSource {
	return &organizationDataSource{}
}

type organizationDataSource struct {
	api *wekaapi.API
}

func (d *organizationDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization"
}

func (d *organizationDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("name"))}
}

func (d *organizationDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Look up an existing organization by UID or name. The API does not return the admin username or password, so those stay null.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Organization UID. Exactly one of id or name is required.",
				Optional:    true,
				Computed:    true,
			},
			"name": schema.StringAttribute{
				Description: "Organization name. Exactly one of id or name is required.",
				Optional:    true,
				Computed:    true,
			},
			"username": schema.StringAttribute{
				Description: "Not returned by the API; always null here.",
				Computed:    true,
			},
			"password": schema.StringAttribute{
				Description: "Not returned by the API; always null here.",
				Computed:    true,
				Sensitive:   true,
			},
			"ssd_quota": schema.Int64Attribute{
				Description: "SSD quota in bytes.",
				Computed:    true,
			},
			"total_quota": schema.Int64Attribute{
				Description: "Total quota in bytes.",
				Computed:    true,
			},
		},
	}
}

func (d *organizationDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.api = configuredAPI(req.ProviderData, &resp.Diagnostics)
}

func (d *organizationDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data organizationModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	want := data.ID.ValueString()
	if want == "" {
		want = data.Name.ValueString()
	}

	out, err := d.api.GetOrganizationsWithResponse(ctx)
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading organization", err.Error())
		return
	}
	if out.JSON200 != nil && out.JSON200.Data != nil {
		for _, o := range *out.JSON200.Data {
			if deref(o.Uid) == want || deref(o.Name) == want {
				resp.Diagnostics.Append(resp.State.Set(ctx, organizationFromAPI(o))...)
				return
			}
		}
	}
	resp.Diagnostics.AddError("Reading organization", fmt.Sprintf("no organization with UID or name %q", want))
}
