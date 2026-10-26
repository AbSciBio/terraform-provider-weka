package provider

import (
	"context"
	"os"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"gitlab.com/absci/devops/terraform-weka/internal/wekaapi"
)

var _ provider.Provider = (*wekaProvider)(nil)

// New returns the provider factory providerserver.Serve expects.
func New(version string) func() provider.Provider {
	return func() provider.Provider { return &wekaProvider{version: version} }
}

type wekaProvider struct {
	version string
}

type providerModel struct {
	Endpoint types.String `tfsdk:"endpoint"`
	Org      types.String `tfsdk:"org"`
	Username types.String `tfsdk:"username"`
	Password types.String `tfsdk:"password"`
	Insecure types.Bool   `tfsdk:"insecure"`
}

func (p *wekaProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "weka"
	resp.Version = p.version
}

func (p *wekaProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Configures a running Weka cluster through its REST API (filesystems, quotas, snapshot policies, S3, users). Cluster layout belongs to the Weka operator, not this provider.",
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				Description: "Cluster URL, e.g. `https://weka01:14000`. `/api/v2` is appended if missing. Env: `WEKA_ENDPOINT`.",
				Optional:    true,
			},
			"org": schema.StringAttribute{
				Description: "Organization to log in to. Defaults to `Root` on the server. Env: `WEKA_ORG`.",
				Optional:    true,
			},
			"username": schema.StringAttribute{
				Description: "Env: `WEKA_USERNAME`.",
				Optional:    true,
			},
			"password": schema.StringAttribute{
				Description: "Env: `WEKA_PASSWORD`.",
				Optional:    true,
				Sensitive:   true,
			},
			"insecure": schema.BoolAttribute{
				Description: "Skip TLS verification. Weka clusters ship a self-signed certificate. Env: `WEKA_INSECURE`.",
				Optional:    true,
			},
		},
	}
}

func (p *wekaProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	for name, v := range map[string]types.String{"endpoint": cfg.Endpoint, "org": cfg.Org, "username": cfg.Username, "password": cfg.Password} {
		if v.IsUnknown() {
			resp.Diagnostics.AddAttributeError(path.Root(name), "Unknown provider value", "The weka provider needs "+name+" at plan time. Read it from a data source or variable, not from a resource created in the same apply.")
		}
	}
	if cfg.Insecure.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("insecure"), "Unknown provider value", "insecure must be known at plan time.")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	insecure := cfg.Insecure.ValueBool()
	if cfg.Insecure.IsNull() {
		insecure, _ = strconv.ParseBool(os.Getenv("WEKA_INSECURE"))
	}
	api, err := wekaapi.New(wekaapi.Config{
		Endpoint:           stringOrEnv(cfg.Endpoint, "WEKA_ENDPOINT"),
		Org:                stringOrEnv(cfg.Org, "WEKA_ORG"),
		Username:           stringOrEnv(cfg.Username, "WEKA_USERNAME"),
		Password:           stringOrEnv(cfg.Password, "WEKA_PASSWORD"),
		InsecureSkipVerify: insecure,
	})
	if err != nil {
		resp.Diagnostics.AddError("Configuring the weka provider", err.Error())
		return
	}
	resp.ResourceData = api
	resp.DataSourceData = api
}

func stringOrEnv(v types.String, env string) string {
	if v.IsNull() {
		return os.Getenv(env)
	}
	return v.ValueString()
}

func (p *wekaProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewFileSystemGroupResource,
	}
}

func (p *wekaProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return nil
}
