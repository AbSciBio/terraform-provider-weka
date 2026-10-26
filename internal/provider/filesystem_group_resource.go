package provider

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/hashicorp/terraform-plugin-framework/path"

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
	_ resource.Resource                   = (*fileSystemGroupResource)(nil)
	_ resource.ResourceWithConfigure      = (*fileSystemGroupResource)(nil)
	_ resource.ResourceWithImportState    = (*fileSystemGroupResource)(nil)
	_ resource.ResourceWithValidateConfig = (*fileSystemGroupResource)(nil)
)

func NewFileSystemGroupResource() resource.Resource {
	return &fileSystemGroupResource{}
}

type fileSystemGroupResource struct {
	api *wekaapi.API
}

type fileSystemGroupModel struct {
	ID                 types.String `tfsdk:"id"`
	Name               types.String `tfsdk:"name"`
	TargetSSDRetention types.Int64  `tfsdk:"target_ssd_retention"`
	StartDemote        types.Int64  `tfsdk:"start_demote"`
}

func (r *fileSystemGroupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_filesystem_group"
}

func (r *fileSystemGroupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A filesystem group: the tiering policy (SSD retention and demote timing) shared by the filesystems in it.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description:   "Filesystem group UID.",
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Description: "Filesystem group name. Renaming updates in place.",
				Required:    true,
			},
			"target_ssd_retention": schema.Int64Attribute{
				Description:   "Seconds data is kept on SSD before it is released to the object store. Server default on create; removing this later keeps the current value rather than reverting, since the API has no way to ask for the default back.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
				Validators:    []validator.Int64{int64validator.Between(0, maxExactSeconds)},
			},
			"start_demote": schema.Int64Attribute{
				Description:   "Seconds after a write before data is copied to the object store. Server default on create; removing this later keeps the current value rather than reverting, since the API has no way to ask for the default back.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
				Validators:    []validator.Int64{int64validator.Between(0, maxExactSeconds)},
			},
		},
	}
}

func (r *fileSystemGroupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.api = configuredAPI(req.ProviderData, &resp.Diagnostics)
}

// Weka returns HTTP 400 ("must specify both demotion and retention time") when
// exactly one of the two is set. Omitting both lets the server apply its default.
func (r *fileSystemGroupResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data fileSystemGroupModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() || data.TargetSSDRetention.IsUnknown() || data.StartDemote.IsUnknown() {
		return
	}
	if data.TargetSSDRetention.IsNull() != data.StartDemote.IsNull() {
		resp.Diagnostics.AddError(
			"Filesystem group policy is incomplete",
			"Set both target_ssd_retention and start_demote, or neither. Weka rejects a group with only one of them.",
		)
		return
	}
	if data.TargetSSDRetention.IsNull() {
		return
	}
	// "Start Demote is larger than SSD Retention / 3" is a 400 from the API.
	retention, demote := data.TargetSSDRetention.ValueInt64(), data.StartDemote.ValueInt64()
	if demote > retention/3 {
		resp.Diagnostics.AddAttributeError(
			path.Root("start_demote"),
			"Filesystem group policy is illegal",
			fmt.Sprintf("start_demote (%d) must be at most target_ssd_retention / 3 (%d).", demote, retention/3),
		)
	}
}

func (r *fileSystemGroupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan fileSystemGroupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	out, err := r.api.CreateFileSystemGroupWithResponse(ctx, wekaapi.CreateFileSystemGroupJSONRequestBody{
		Name:               plan.Name.ValueString(),
		TargetSsdRetention: secondsPtr(plan.TargetSSDRetention),
		StartDemote:        secondsPtr(plan.StartDemote),
	})
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err == nil && (out.JSON200 == nil || out.JSON200.Data == nil || out.JSON200.Data.Uid == nil) {
		err = errors.New("create response has no uid")
	}
	if err != nil {
		resp.Diagnostics.AddError("Creating filesystem group", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, fileSystemGroupFromAPI(*out.JSON200.Data))...)
}

func (r *fileSystemGroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state fileSystemGroupModel
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
		resp.Diagnostics.AddError("Reading filesystem group", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *fileSystemGroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state fileSystemGroupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body := wekaapi.UpdateFileSystemGroupJSONRequestBody{
		TargetSsdRetention: secondsPtr(plan.TargetSSDRetention),
		StartDemote:        secondsPtr(plan.StartDemote),
	}
	if !plan.Name.Equal(state.Name) {
		body.NewName = plan.Name.ValueStringPointer()
	}
	out, err := r.api.UpdateFileSystemGroupWithResponse(ctx, state.ID.ValueString(), body)
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	var m fileSystemGroupModel
	if err == nil {
		m, err = r.get(ctx, state.ID.ValueString())
	}
	if err != nil {
		resp.Diagnostics.AddError("Updating filesystem group", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *fileSystemGroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state fileSystemGroupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	out, err := r.api.DeleteFileSystemGroupWithResponse(ctx, state.ID.ValueString())
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil && !wekaapi.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting filesystem group", err.Error())
	}
}

// ImportState accepts a UID or a name, so existing groups can be adopted by
// the name people already know.
func (r *fileSystemGroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	out, err := r.api.GetFileSystemGroupsWithResponse(ctx)
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil {
		resp.Diagnostics.AddError("Importing filesystem group", err.Error())
		return
	}
	if out.JSON200 != nil && out.JSON200.Data != nil {
		for _, g := range *out.JSON200.Data {
			if deref(g.Uid) == req.ID || deref(g.Name) == req.ID {
				resp.Diagnostics.Append(resp.State.Set(ctx, fileSystemGroupFromAPI(g))...)
				return
			}
		}
	}
	resp.Diagnostics.AddError("Importing filesystem group", fmt.Sprintf("no filesystem group with UID or name %q", req.ID))
}

func (r *fileSystemGroupResource) get(ctx context.Context, uid string) (fileSystemGroupModel, error) {
	out, err := r.api.GetFileSystemGroupWithResponse(ctx, uid)
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil {
		return fileSystemGroupModel{}, err
	}
	if out.JSON200 == nil || out.JSON200.Data == nil {
		return fileSystemGroupModel{}, errors.New("get response has no data")
	}
	return fileSystemGroupFromAPI(*out.JSON200.Data), nil
}

func fileSystemGroupFromAPI(g wekaapi.FileSystemGroup) fileSystemGroupModel {
	return fileSystemGroupModel{
		ID:                 types.StringValue(deref(g.Uid)),
		Name:               types.StringValue(deref(g.Name)),
		TargetSSDRetention: secondsValue(g.TargetSsdRetention),
		StartDemote:        secondsValue(g.StartDemote),
	}
}

// The spec types these second counts as float32, which loses precision above
// 2^24 (about 194 days): 16777217 round-trips as 16777216. The schema
// validators reject anything past that so a value is never silently altered.
const maxExactSeconds = 1 << 24

func secondsPtr(v types.Int64) *float32 {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	f := float32(v.ValueInt64())
	return &f
}

func secondsValue(f *float32) types.Int64 {
	if f == nil {
		return types.Int64Null()
	}
	return types.Int64Value(int64(math.Round(float64(*f))))
}
