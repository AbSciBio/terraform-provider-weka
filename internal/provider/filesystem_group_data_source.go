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
	_ datasource.DataSource                     = (*fileSystemGroupDataSource)(nil)
	_ datasource.DataSourceWithConfigure        = (*fileSystemGroupDataSource)(nil)
	_ datasource.DataSourceWithConfigValidators = (*fileSystemGroupDataSource)(nil)
)

func NewFileSystemGroupDataSource() datasource.DataSource {
	return &fileSystemGroupDataSource{}
}

type fileSystemGroupDataSource struct {
	api *wekaapi.API
}

func (d *fileSystemGroupDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_filesystem_group"
}

func (d *fileSystemGroupDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("name"))}
}

func (d *fileSystemGroupDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Look up an existing filesystem group by UID or name.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Filesystem group UID. Exactly one of id or name is required.",
				Optional:    true,
				Computed:    true,
			},
			"name": schema.StringAttribute{
				Description: "Filesystem group name. Exactly one of id or name is required.",
				Optional:    true,
				Computed:    true,
			},
			"target_ssd_retention": schema.Int64Attribute{
				Description: "Seconds data is kept on SSD before it is released to the object store.",
				Computed:    true,
			},
			"start_demote": schema.Int64Attribute{
				Description: "Seconds after a write before data is copied to the object store.",
				Computed:    true,
			},
		},
	}
}

func (d *fileSystemGroupDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.api = configuredAPI(req.ProviderData, &resp.Diagnostics)
}

func (d *fileSystemGroupDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data fileSystemGroupModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	want := data.ID.ValueString()
	if want == "" {
		want = data.Name.ValueString()
	}

	out, err := d.api.GetFileSystemGroupsWithResponse(ctx)
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading filesystem group", err.Error())
		return
	}
	if out.JSON200 != nil && out.JSON200.Data != nil {
		for _, g := range *out.JSON200.Data {
			if deref(g.Uid) == want || deref(g.Name) == want {
				resp.Diagnostics.Append(resp.State.Set(ctx, fileSystemGroupFromAPI(g))...)
				return
			}
		}
	}
	resp.Diagnostics.AddError("Reading filesystem group", fmt.Sprintf("no filesystem group with UID or name %q", want))
}
