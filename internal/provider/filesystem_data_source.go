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
	_ datasource.DataSource                     = (*fileSystemDataSource)(nil)
	_ datasource.DataSourceWithConfigure        = (*fileSystemDataSource)(nil)
	_ datasource.DataSourceWithConfigValidators = (*fileSystemDataSource)(nil)
)

func NewFileSystemDataSource() datasource.DataSource {
	return &fileSystemDataSource{}
}

type fileSystemDataSource struct {
	api *wekaapi.API
}

func (d *fileSystemDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_filesystem"
}

func (d *fileSystemDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("name"))}
}

func (d *fileSystemDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Look up an existing filesystem by UID or name.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Filesystem UID. Exactly one of id or name is required.",
				Optional:    true,
				Computed:    true,
			},
			"name": schema.StringAttribute{
				Description: "Filesystem name. Exactly one of id or name is required.",
				Optional:    true,
				Computed:    true,
			},
			"group_name": schema.StringAttribute{
				Description: "Name of the filesystem group this filesystem belongs to.",
				Computed:    true,
			},
			"total_capacity": schema.Int64Attribute{
				Description: "Total capacity in bytes.",
				Computed:    true,
			},
			"ssd_capacity": schema.Int64Attribute{
				Description: "SSD capacity in bytes.",
				Computed:    true,
			},
			"auth_required": schema.BoolAttribute{
				Description: "Whether an authenticated user is required to mount this filesystem.",
				Computed:    true,
			},
			"encrypted": schema.BoolAttribute{
				Description: "Whether the filesystem is encrypted.",
				Computed:    true,
			},
		},
	}
}

func (d *fileSystemDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.api = configuredAPI(req.ProviderData, &resp.Diagnostics)
}

func (d *fileSystemDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data fileSystemModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	want := data.ID.ValueString()
	if want == "" {
		want = data.Name.ValueString()
	}

	out, err := d.api.GetFileSystemsWithResponse(ctx, forceFreshList())
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading filesystem", err.Error())
		return
	}
	if out.JSON200 != nil && out.JSON200.Data != nil {
		for _, fs := range *out.JSON200.Data {
			if deref(fs.Uid) == want || deref(fs.Name) == want {
				resp.Diagnostics.Append(resp.State.Set(ctx, fileSystemFromAPI(fs))...)
				return
			}
		}
	}
	resp.Diagnostics.AddError("Reading filesystem", fmt.Sprintf("no filesystem with UID or name %q", want))
}
