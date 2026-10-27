package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"

	"gitlab.com/absci/devops/terraform-provider-weka/internal/wekaapi"
)

var (
	_ datasource.DataSource                     = (*userDataSource)(nil)
	_ datasource.DataSourceWithConfigure        = (*userDataSource)(nil)
	_ datasource.DataSourceWithConfigValidators = (*userDataSource)(nil)
)

func NewUserDataSource() datasource.DataSource {
	return &userDataSource{}
}

type userDataSource struct {
	api *wekaapi.API
}

func (d *userDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user"
}

func (d *userDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("username"))}
}

func (d *userDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Look up an existing user by UID or username. The API does not return the password, so it stays null.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "User UID. Exactly one of id or username is required.",
				Optional:    true,
				Computed:    true,
			},
			"username": schema.StringAttribute{
				Description: "User name. Exactly one of id or username is required.",
				Optional:    true,
				Computed:    true,
			},
			"password": schema.StringAttribute{
				Description: "Not returned by the API; always null here.",
				Computed:    true,
				Sensitive:   true,
			},
			"role": schema.StringAttribute{
				Description: "Role of the user.",
				Computed:    true,
			},
			"posix_uid": schema.Int64Attribute{
				Description: "POSIX UID for this user's S3 files.",
				Computed:    true,
			},
			"posix_gid": schema.Int64Attribute{
				Description: "POSIX GID for this user's S3 files.",
				Computed:    true,
			},
		},
	}
}

func (d *userDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.api = configuredAPI(req.ProviderData, &resp.Diagnostics)
}

func (d *userDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data userModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	want := data.ID.ValueString()
	if want == "" {
		want = data.Username.ValueString()
	}

	// Reuses userResource.list: there is no get-by-uid, so both the resource
	// and this data source list and match on uid or username.
	users, err := (&userResource{api: d.api}).list(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading user", err.Error())
		return
	}
	for _, u := range users {
		if deref(u.Uid) == want || deref(u.Username) == want {
			resp.Diagnostics.Append(resp.State.Set(ctx, userFromAPI(u))...)
			return
		}
	}
	resp.Diagnostics.AddError("Reading user", fmt.Sprintf("no user with UID or username %q", want))
}
