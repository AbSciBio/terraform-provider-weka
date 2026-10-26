package provider

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"gitlab.com/absci/devops/terraform-provider-weka/internal/wekaapi"
)

var (
	_ resource.Resource                = (*organizationResource)(nil)
	_ resource.ResourceWithConfigure   = (*organizationResource)(nil)
	_ resource.ResourceWithImportState = (*organizationResource)(nil)
)

func NewOrganizationResource() resource.Resource {
	return &organizationResource{}
}

type organizationResource struct {
	api *wekaapi.API
}

type organizationModel struct {
	ID         types.String `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	Username   types.String `tfsdk:"username"`
	Password   types.String `tfsdk:"password"`
	SSDQuota   types.Int64  `tfsdk:"ssd_quota"`
	TotalQuota types.Int64  `tfsdk:"total_quota"`
}

func (r *organizationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization"
}

func (r *organizationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "An organization, with the admin user created for it and optional SSD and total capacity quotas.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description:   "Organization UID.",
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Description: "Organization name. Renaming updates in place.",
				Required:    true,
			},
			"username": schema.StringAttribute{
				Description:   "Username of the organization admin. Changing it replaces the organization. The API does not return this value, so Terraform keeps the configured one, and an import records it on the next apply.",
				Required:      true,
				PlanModifiers: []planmodifier.String{replaceWhenKnown()},
			},
			"password": schema.StringAttribute{
				Description:   "Password of the organization admin. Changing it replaces the organization. The API does not return this value, so Terraform keeps the configured one, and an import records it on the next apply.",
				Required:      true,
				Sensitive:     true,
				PlanModifiers: []planmodifier.String{replaceWhenKnown()},
			},
			"ssd_quota": schema.Int64Attribute{
				Description:   "SSD quota. The API describes a capacity such as 1GiB, and types the field as float32, which stores integers exactly only through 16777216 (2^24). Values must be from 0 to 16777216 so a quota is never silently rounded. Server default on create. Removing this later keeps the current value, since the API has no way to ask for the default back.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
				Validators:    []validator.Int64{int64validator.Between(0, 1<<24)},
			},
			"total_quota": schema.Int64Attribute{
				Description:   "Total quota. The API describes a capacity such as 1GiB, and types the field as float32, which stores integers exactly only through 16777216 (2^24). Values must be from 0 to 16777216 so a quota is never silently rounded. Server default on create. Removing this later keeps the current value, since the API has no way to ask for the default back.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
				Validators:    []validator.Int64{int64validator.Between(0, 1<<24)},
			},
		},
	}
}

func (r *organizationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.api = configuredAPI(req.ProviderData, &resp.Diagnostics)
}

func (r *organizationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan organizationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	out, err := r.api.CreateOrganizationWithResponse(ctx, wekaapi.CreateOrganizationJSONRequestBody{
		Name:       plan.Name.ValueString(),
		Username:   plan.Username.ValueString(),
		Password:   plan.Password.ValueString(),
		SsdQuota:   quotaPtr(plan.SSDQuota),
		TotalQuota: quotaPtr(plan.TotalQuota),
	})
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err == nil && (out.JSON200 == nil || out.JSON200.Data == nil || out.JSON200.Data.Uid == nil) {
		err = errors.New("create response has no uid")
	}
	if err != nil {
		resp.Diagnostics.AddError("Creating organization", err.Error())
		return
	}
	m := organizationFromAPI(*out.JSON200.Data)
	m.Username = plan.Username
	m.Password = plan.Password
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *organizationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state organizationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	m, err := r.get(ctx, state.ID.ValueString())
	if wekaapi.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading organization", err.Error())
		return
	}
	m.Username = state.Username
	m.Password = state.Password
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *organizationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state organizationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	uid := state.ID.ValueString()
	if !plan.Name.Equal(state.Name) {
		out, err := r.api.UpdateOrganizationWithResponse(ctx, uid, wekaapi.UpdateOrganizationJSONRequestBody{
			NewName: plan.Name.ValueStringPointer(),
		})
		if err == nil {
			err = wekaapi.Check(out.StatusCode(), out.Body)
		}
		if err != nil {
			resp.Diagnostics.AddError("Updating organization", err.Error())
			return
		}
	}
	if !plan.SSDQuota.Equal(state.SSDQuota) || !plan.TotalQuota.Equal(state.TotalQuota) {
		out, err := r.api.SetOrganizationLimitWithResponse(ctx, uid, wekaapi.SetOrganizationLimitJSONRequestBody{
			SsdQuota:   quotaPtr(plan.SSDQuota),
			TotalQuota: quotaPtr(plan.TotalQuota),
		})
		if err == nil {
			err = wekaapi.Check(out.StatusCode(), out.Body)
		}
		if err != nil {
			resp.Diagnostics.AddError("Updating organization", err.Error())
			return
		}
	}

	m, err := r.get(ctx, uid)
	if err != nil {
		resp.Diagnostics.AddError("Updating organization", err.Error())
		return
	}
	m.Username = plan.Username
	m.Password = plan.Password
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *organizationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state organizationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	out, err := r.api.DeleteOrganizationWithResponse(ctx, state.ID.ValueString())
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil && !wekaapi.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting organization", err.Error())
	}
}

// ImportState accepts a UID or a name, so existing organizations can be adopted
// by the name people already know. Username and password are not in the API
// response; the next apply stores them from configuration.
func (r *organizationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	out, err := r.api.GetOrganizationsWithResponse(ctx)
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil {
		resp.Diagnostics.AddError("Importing organization", err.Error())
		return
	}
	if out.JSON200 != nil && out.JSON200.Data != nil {
		for _, o := range *out.JSON200.Data {
			if deref(o.Uid) == req.ID || deref(o.Name) == req.ID {
				resp.Diagnostics.Append(resp.State.Set(ctx, organizationFromAPI(o))...)
				return
			}
		}
	}
	resp.Diagnostics.AddError("Importing organization", fmt.Sprintf("no organization with UID or name %q", req.ID))
}

func (r *organizationResource) get(ctx context.Context, uid string) (organizationModel, error) {
	out, err := r.api.GetOrganizationWithResponse(ctx, uid)
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil {
		return organizationModel{}, err
	}
	if out.JSON200 == nil || out.JSON200.Data == nil {
		return organizationModel{}, errors.New("get response has no data")
	}
	return organizationFromAPI(*out.JSON200.Data), nil
}

func organizationFromAPI(o wekaapi.Organization) organizationModel {
	return organizationModel{
		ID:         types.StringValue(deref(o.Uid)),
		Name:       types.StringValue(deref(o.Name)),
		SSDQuota:   quotaValue(o.SsdQuota),
		TotalQuota: quotaValue(o.TotalQuota),
	}
}

// replaceWhenKnown is ForceNew for a value the API never returns. A null
// prior value is an import, and filling it from configuration must not
// create a second organization.
func replaceWhenKnown() planmodifier.String {
	return stringplanmodifier.RequiresReplaceIf(
		func(_ context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
			if req.StateValue.IsNull() || req.StateValue.IsUnknown() || req.PlanValue.IsUnknown() {
				return
			}
			resp.RequiresReplace = true
		},
		"Changing this value replaces the organization.",
		"Changing this value replaces the organization.",
	)
}

// The spec types these quotas as float32, which loses precision above 2^24:
// 16777217 round-trips as 16777216. The schema validators reject anything
// past that so a value is never silently altered.
func quotaPtr(v types.Int64) *float32 {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	f := float32(v.ValueInt64())
	return &f
}

func quotaValue(f *float32) types.Int64 {
	if f == nil {
		return types.Int64Null()
	}
	return types.Int64Value(int64(math.Round(float64(*f))))
}
