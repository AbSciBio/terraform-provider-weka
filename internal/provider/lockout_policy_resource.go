package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"gitlab.com/absci/devops/terraform-provider-weka/internal/wekaapi"
)

// One lockout policy per cluster. The id is not a server field.
const lockoutPolicyID = "cluster"

var (
	_ resource.Resource                = (*lockoutPolicyResource)(nil)
	_ resource.ResourceWithConfigure   = (*lockoutPolicyResource)(nil)
	_ resource.ResourceWithImportState = (*lockoutPolicyResource)(nil)
)

func NewLockoutPolicyResource() resource.Resource {
	return &lockoutPolicyResource{}
}

type lockoutPolicyResource struct {
	api *wekaapi.API
}

type lockoutPolicyModel struct {
	ID              types.String `tfsdk:"id"`
	LockoutDuration types.Int64  `tfsdk:"lockout_duration"`
	NumFailedLogins types.Int64  `tfsdk:"num_failed_logins"`
}

func (r *lockoutPolicyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_lockout_policy"
}

func (r *lockoutPolicyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	// lockout_duration is seconds: the spec example is 120 and the documented
	// default lock is 2 minutes. The 4.4 schema does not name the unit.
	resp.Schema = schema.Schema{
		Description: "The cluster account lockout policy. There is one per cluster. Destroy resets it.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description:   "Always `cluster`.",
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"lockout_duration": schema.Int64Attribute{
				Description: "Seconds an account stays locked after too many failed sign-ins.",
				Required:    true,
				Validators:  []validator.Int64{int64validator.Between(0, maxExactSeconds)},
			},
			"num_failed_logins": schema.Int64Attribute{
				Description: "Failed sign-ins before an account is locked.",
				Required:    true,
				Validators:  []validator.Int64{int64validator.Between(0, maxExactSeconds)},
			},
		},
	}
}

func (r *lockoutPolicyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.api = configuredAPI(req.ProviderData, &resp.Diagnostics)
}

func (r *lockoutPolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan lockoutPolicyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	m, err := r.set(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Setting lockout policy", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *lockoutPolicyResource) Read(ctx context.Context, _ resource.ReadRequest, resp *resource.ReadResponse) {
	m, err := r.get(ctx)
	if wekaapi.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading lockout policy", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *lockoutPolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan lockoutPolicyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	m, err := r.set(ctx, plan)
	if err != nil {
		resp.Diagnostics.AddError("Setting lockout policy", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

// Delete calls resetLockoutPolicy. The 4.4 spec documents 200 and 401 only;
// a 404 is success. Published docs disagree on the side effect: the REST text
// says this clears failed-login counters and unlocks accounts, while the CLI
// text for the mapped command says it restores the default threshold and
// duration. Not checked on a cluster.
func (r *lockoutPolicyResource) Delete(ctx context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	out, err := r.api.ResetLockoutPolicyWithResponse(ctx)
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil && !wekaapi.IsNotFound(err) {
		resp.Diagnostics.AddError("Resetting lockout policy", err.Error())
	}
}

func (r *lockoutPolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != lockoutPolicyID {
		resp.Diagnostics.AddError("Importing lockout policy", fmt.Sprintf("import id must be %q", lockoutPolicyID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), lockoutPolicyID)...)
}

func (r *lockoutPolicyResource) set(ctx context.Context, plan lockoutPolicyModel) (lockoutPolicyModel, error) {
	out, err := r.api.SetLockoutPolicyWithResponse(ctx, wekaapi.LockoutPolicy{
		LockoutDuration: secondsPtr(plan.LockoutDuration),
		NumFailedLogins: secondsPtr(plan.NumFailedLogins),
	})
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil {
		return lockoutPolicyModel{}, err
	}
	if out.JSON200 == nil || out.JSON200.Data == nil {
		return lockoutPolicyModel{}, errors.New("set response has no data")
	}
	return lockoutPolicyFromAPI(*out.JSON200.Data)
}

func (r *lockoutPolicyResource) get(ctx context.Context) (lockoutPolicyModel, error) {
	out, err := r.api.GetLockoutPolicyWithResponse(ctx)
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil {
		return lockoutPolicyModel{}, err
	}
	if out.JSON200 == nil || out.JSON200.Data == nil {
		return lockoutPolicyModel{}, errors.New("get response has no data")
	}
	return lockoutPolicyFromAPI(*out.JSON200.Data)
}

func lockoutPolicyFromAPI(p wekaapi.LockoutPolicy) (lockoutPolicyModel, error) {
	if p.LockoutDuration == nil || p.NumFailedLogins == nil {
		return lockoutPolicyModel{}, errors.New("lockout policy response is missing lockout_duration or num_failed_logins")
	}
	return lockoutPolicyModel{
		ID:              types.StringValue(lockoutPolicyID),
		LockoutDuration: secondsValue(p.LockoutDuration),
		NumFailedLogins: secondsValue(p.NumFailedLogins),
	}, nil
}
