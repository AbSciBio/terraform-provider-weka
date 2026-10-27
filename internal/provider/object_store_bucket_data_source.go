package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/AbSciBio/terraform-provider-weka/internal/wekaapi"
)

var (
	_ datasource.DataSource                     = (*objectStoreBucketDataSource)(nil)
	_ datasource.DataSourceWithConfigure        = (*objectStoreBucketDataSource)(nil)
	_ datasource.DataSourceWithConfigValidators = (*objectStoreBucketDataSource)(nil)
)

func NewObjectStoreBucketDataSource() datasource.DataSource {
	return &objectStoreBucketDataSource{}
}

type objectStoreBucketDataSource struct {
	api *wekaapi.API
}

func (d *objectStoreBucketDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_object_store_bucket"
}

func (d *objectStoreBucketDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("name"))}
}

func (d *objectStoreBucketDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Look up an existing object store bucket by UID or name. The API only returns the secret key when it is not masked, so it is often null here.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Object store bucket UID. Exactly one of id or name is required.",
				Optional:    true,
				Computed:    true,
			},
			"name": schema.StringAttribute{
				Description: "Object store bucket name. Exactly one of id or name is required.",
				Optional:    true,
				Computed:    true,
			},
			"obs_name": schema.StringAttribute{
				Description: "Object store this bucket belongs to.",
				Computed:    true,
			},
			"obs_site": schema.StringAttribute{
				Description: "Site of the bucket (local or remote).",
				Computed:    true,
			},
			"obs_type": schema.StringAttribute{
				Description: "Object store type.",
				Computed:    true,
			},
			"protocol": schema.StringAttribute{
				Description: "Protocol used to reach the object store.",
				Computed:    true,
			},
			"hostname": schema.StringAttribute{
				Description: "Hostname or IP of the object store endpoint.",
				Computed:    true,
			},
			"port": schema.Int64Attribute{
				Description: "Port of the object store endpoint.",
				Computed:    true,
			},
			"bucket": schema.StringAttribute{
				Description: "Bucket name in the object store.",
				Computed:    true,
			},
			"auth_method": schema.StringAttribute{
				Description: "Authentication method.",
				Computed:    true,
			},
			"region": schema.StringAttribute{
				Description: "Region the bucket is assigned to.",
				Computed:    true,
			},
			"access_key_id": schema.StringAttribute{
				Description: "Access key ID for AWS Signature authentication.",
				Computed:    true,
			},
			"secret_key": schema.StringAttribute{
				Description: "Secret key for AWS Signature authentication, when the API does not mask it.",
				Computed:    true,
				Sensitive:   true,
			},
		},
	}
}

func (d *objectStoreBucketDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.api = configuredAPI(req.ProviderData, &resp.Diagnostics)
}

func (d *objectStoreBucketDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data objectStoreBucketModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	want := data.ID.ValueString()
	if want == "" {
		want = data.Name.ValueString()
	}

	out, err := d.api.GetAllObsBucketsWithResponse(ctx)
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading object store bucket", err.Error())
		return
	}
	if out.JSON200 != nil && out.JSON200.Data != nil {
		for _, b := range *out.JSON200.Data {
			if deref(b.Uid) == want || deref(b.Name) == want {
				resp.Diagnostics.Append(resp.State.Set(ctx, objectStoreBucketFromAPI(b, types.StringNull()))...)
				return
			}
		}
	}
	resp.Diagnostics.AddError("Reading object store bucket", fmt.Sprintf("no object store bucket with UID or name %q", want))
}
