package provider

import (
	"context"
	"errors"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/AbSciBio/terraform-provider-weka/internal/wekaapi"
)

// One LDAP configuration per cluster. The API has no uid for it.
const ldapID = "cluster"

var (
	_ resource.Resource                = (*ldapResource)(nil)
	_ resource.ResourceWithConfigure   = (*ldapResource)(nil)
	_ resource.ResourceWithImportState = (*ldapResource)(nil)
)

func NewLdapResource() resource.Resource {
	return &ldapResource{}
}

type ldapResource struct {
	api *wekaapi.API
}

type ldapModel struct {
	ID                       types.String `tfsdk:"id"`
	Enabled                  types.Bool   `tfsdk:"enabled"`
	ServerURI                types.String `tfsdk:"server_uri"`
	BaseDN                   types.String `tfsdk:"base_dn"`
	UserObjectClass          types.String `tfsdk:"user_object_class"`
	UserIDAttribute          types.String `tfsdk:"user_id_attribute"`
	GroupObjectClass         types.String `tfsdk:"group_object_class"`
	GroupIDAttribute         types.String `tfsdk:"group_id_attribute"`
	GroupMembershipAttribute types.String `tfsdk:"group_membership_attribute"`
	ReaderUsername           types.String `tfsdk:"reader_username"`
	ReaderPassword           types.String `tfsdk:"reader_password"`
}

func (r *ldapResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ldap"
}

func (r *ldapResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The cluster LDAP configuration. There is one per cluster; the id and the import id are `cluster`. Delete resets it.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description:   "Always `cluster`.",
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"enabled": schema.BoolAttribute{
				Description: "Whether the cluster uses LDAP for authentication.",
				Required:    true,
			},
			"server_uri": schema.StringAttribute{
				Description: "LDAP server URI (`[ldap://]hostname[:port]` or `ldaps://hostname[:port]`).",
				Required:    true,
			},
			"base_dn": schema.StringAttribute{
				Description: "Base DN for LDAP searches.",
				Required:    true,
			},
			"user_object_class": schema.StringAttribute{
				Description: "Object class of a user entry.",
				Required:    true,
			},
			"user_id_attribute": schema.StringAttribute{
				Description: "Attribute used as the user id.",
				Required:    true,
			},
			"group_object_class": schema.StringAttribute{
				Description: "Object class of a group entry.",
				Required:    true,
			},
			"group_id_attribute": schema.StringAttribute{
				Description: "Attribute used as the group id.",
				Required:    true,
			},
			"group_membership_attribute": schema.StringAttribute{
				Description: "Attribute listing the members of a group.",
				Required:    true,
			},
			"reader_username": schema.StringAttribute{
				Description: "Account the cluster uses to query the directory.",
				Required:    true,
			},
			"reader_password": schema.StringAttribute{
				Description: "Password for `reader_username`. GET /ldap does not return it; Terraform keeps the configured value.",
				Required:    true,
				Sensitive:   true,
			},
		},
	}
}

func (r *ldapResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.api = configuredAPI(req.ProviderData, &resp.Diagnostics)
}

func (r *ldapResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ldapModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	m, err := r.put(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Creating LDAP configuration", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *ldapResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ldapModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	m, err := r.get(ctx, state.ReaderPassword)
	if wekaapi.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading LDAP configuration", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *ldapResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan ldapModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	m, err := r.put(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Updating LDAP configuration", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *ldapResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state ldapModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	out, err := r.api.DeleteLdapWithResponse(ctx)
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil && !wekaapi.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting LDAP configuration", err.Error())
	}
}

func (r *ldapResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != ldapID {
		resp.Diagnostics.AddError("Importing LDAP configuration", `import id must be "cluster"`)
		return
	}
	// Read fills the rest. GET does not return the password, so it stays null
	// until the next apply sets it from configuration.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), ldapID)...)
}

func (r *ldapResource) put(ctx context.Context, plan ldapModel) (ldapModel, error) {
	// start_tls, protocol_version, server_timeout_secs, and role_groups are left
	// unset. They are optional on the wire and this resource does not manage them.
	out, err := r.api.UpdateLdapWithResponse(ctx, wekaapi.UpdateLdapJSONRequestBody{
		Enabled:                  plan.Enabled.ValueBoolPointer(),
		ServerUri:                plan.ServerURI.ValueStringPointer(),
		BaseDn:                   plan.BaseDN.ValueStringPointer(),
		UserObjectClass:          plan.UserObjectClass.ValueStringPointer(),
		UserIdAttribute:          plan.UserIDAttribute.ValueStringPointer(),
		GroupObjectClass:         plan.GroupObjectClass.ValueStringPointer(),
		GroupIdAttribute:         plan.GroupIDAttribute.ValueStringPointer(),
		GroupMembershipAttribute: plan.GroupMembershipAttribute.ValueStringPointer(),
		ReaderUsername:           plan.ReaderUsername.ValueStringPointer(),
		ReaderPassword:           plan.ReaderPassword.ValueStringPointer(),
	})
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil {
		return ldapModel{}, err
	}
	return r.get(ctx, plan.ReaderPassword)
}

func (r *ldapResource) get(ctx context.Context, readerPassword types.String) (ldapModel, error) {
	out, err := r.api.GetLdapWithResponse(ctx)
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil {
		return ldapModel{}, err
	}
	if out.JSON200 == nil || out.JSON200.Data == nil {
		return ldapModel{}, errors.New("get response has no data")
	}
	d := out.JSON200.Data
	return ldapModel{
		ID:                       types.StringValue(ldapID),
		Enabled:                  boolAttr(d.Enabled),
		ServerURI:                stringAttr(d.ServerUri),
		BaseDN:                   stringAttr(d.BaseDn),
		UserObjectClass:          stringAttr(d.UserObjectClass),
		UserIDAttribute:          stringAttr(d.UserIdAttribute),
		GroupObjectClass:         stringAttr(d.GroupObjectClass),
		GroupIDAttribute:         stringAttr(d.GroupIdAttribute),
		GroupMembershipAttribute: stringAttr(d.GroupMembershipAttribute),
		ReaderUsername:           stringAttr(d.ReaderUsername),
		// The generated GET model has no reader_password. Keep the configured
		// value so a refresh does not plan to write it again.
		ReaderPassword: readerPassword,
	}, nil
}

func stringAttr(p *string) types.String {
	if p == nil {
		return types.StringNull()
	}
	return types.StringValue(*p)
}

func boolAttr(p *bool) types.Bool {
	if p == nil {
		return types.BoolNull()
	}
	return types.BoolValue(*p)
}
