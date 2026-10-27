package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"gitlab.com/absci/devops/terraform-provider-weka/internal/wekaapi"
)

var (
	_ datasource.DataSource              = (*ldapDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*ldapDataSource)(nil)
)

func NewLdapDataSource() datasource.DataSource {
	return &ldapDataSource{}
}

type ldapDataSource struct {
	api *wekaapi.API
}

func (d *ldapDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ldap"
}

func (d *ldapDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The cluster LDAP configuration. There is one per cluster; the API does not return reader_password, so it stays null here.",
		Attributes: map[string]schema.Attribute{
			"id":                         schema.StringAttribute{Description: "Always `cluster`.", Computed: true},
			"enabled":                    schema.BoolAttribute{Description: "Whether the cluster uses LDAP for authentication.", Computed: true},
			"server_uri":                 schema.StringAttribute{Description: "LDAP server URI.", Computed: true},
			"base_dn":                    schema.StringAttribute{Description: "Base DN for LDAP searches.", Computed: true},
			"user_object_class":          schema.StringAttribute{Description: "Object class of a user entry.", Computed: true},
			"user_id_attribute":          schema.StringAttribute{Description: "Attribute used as the user id.", Computed: true},
			"group_object_class":         schema.StringAttribute{Description: "Object class of a group entry.", Computed: true},
			"group_id_attribute":         schema.StringAttribute{Description: "Attribute used as the group id.", Computed: true},
			"group_membership_attribute": schema.StringAttribute{Description: "Attribute listing the members of a group.", Computed: true},
			"reader_username":            schema.StringAttribute{Description: "Account the cluster uses to query the directory.", Computed: true},
			"reader_password": schema.StringAttribute{
				Description: "Not returned by the API; always null here.",
				Computed:    true,
				Sensitive:   true,
			},
		},
	}
}

func (d *ldapDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.api = configuredAPI(req.ProviderData, &resp.Diagnostics)
}

func (d *ldapDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	// Reuses ldapResource.get, which already handles the response shape and
	// keeps reader_password as whatever is passed in; a data source has no
	// configured value to keep, so that stays null.
	m, err := (&ldapResource{api: d.api}).get(ctx, types.StringNull())
	if err != nil {
		resp.Diagnostics.AddError("Reading LDAP configuration", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}
