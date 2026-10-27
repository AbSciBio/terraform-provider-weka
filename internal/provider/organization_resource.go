package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/AbSciBio/terraform-provider-weka/internal/wekaapi"
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
				Description:   "SSD quota in bytes. The cluster rounds this up to a multiple of 8192, and the API stores it as a float32 whose JSON decimal must be that same integer. A value that would change is rejected at plan time. The cluster also enforces a minimum. Server default on create. Removing this later keeps the current value, since the API has no way to ask for the default back.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
				Validators:    []validator.Int64{exactSeconds(), quotaSendable()},
			},
			"total_quota": schema.Int64Attribute{
				Description:   "Total quota in bytes. The cluster rounds this up to a multiple of 8192, and the API stores it as a float32 whose JSON decimal must be that same integer. A value that would change is rejected at plan time. The cluster also enforces a minimum. Server default on create. Removing this later keeps the current value, since the API has no way to ask for the default back.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
				Validators:    []validator.Int64{exactSeconds(), quotaSendable()},
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

// quotaAlign is the cluster's quota rounding step.
const quotaAlign int64 = 8192

func quotaSendable() validator.Int64 { return quotaSendableValidator{} }

type quotaSendableValidator struct{}

func (quotaSendableValidator) Description(context.Context) string {
	return "must be a multiple of 8192 that float32 JSON can send unchanged"
}

func (quotaSendableValidator) MarkdownDescription(ctx context.Context) string {
	return quotaSendableValidator{}.Description(ctx)
}

func (quotaSendableValidator) ValidateInt64(_ context.Context, req validator.Int64Request, resp *validator.Int64Response) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() || !exactSecondsValue(req.ConfigValue.ValueInt64()) {
		return
	}
	n := req.ConfigValue.ValueInt64()
	if w := quotaWire(n); w != n {
		resp.Diagnostics.AddAttributeError(req.Path, "Quota would change on the cluster",
			fmt.Sprintf("%d would be stored as %d. Set the quota to %d.", n, w, w))
	}
}

// quotaWire is the integer the API will store. The cluster rounds up to a
// multiple of 8192, and encoding/json can emit a different decimal for a
// float32 that is exact in memory. 1 GiB is exact in memory and still goes
// out as 1073741800, which is under the cluster minimum.
func quotaWire(n int64) int64 {
	if n <= 0 {
		return n
	}
	if n%quotaAlign != 0 {
		n += quotaAlign - n%quotaAlign
	}
	for range 4096 {
		if exactSecondsValue(n) && quotaJSONExact(n) {
			return n
		}
		n += quotaAlign
	}
	return n
}

func quotaJSONExact(n int64) bool {
	b, err := json.Marshal(float32(n))
	return err == nil && string(b) == strconv.FormatInt(n, 10)
}

// The spec types these quotas as float32. exactSeconds rejects a value that
// would be stored as a different number. quotaWire then picks a nearby value
// the cluster will keep.
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
