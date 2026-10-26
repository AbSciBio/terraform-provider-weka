package provider

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"gitlab.com/absci/devops/terraform-provider-weka/internal/wekaapi"
)

var (
	_ resource.Resource                = (*fileSystemResource)(nil)
	_ resource.ResourceWithConfigure   = (*fileSystemResource)(nil)
	_ resource.ResourceWithImportState = (*fileSystemResource)(nil)
)

func NewFileSystemResource() resource.Resource {
	return &fileSystemResource{}
}

type fileSystemResource struct {
	api *wekaapi.API
}

type fileSystemModel struct {
	ID            types.String `tfsdk:"id"`
	Name          types.String `tfsdk:"name"`
	GroupName     types.String `tfsdk:"group_name"`
	TotalCapacity types.Int64  `tfsdk:"total_capacity"`
	SSDCapacity   types.Int64  `tfsdk:"ssd_capacity"`
	AuthRequired  types.Bool   `tfsdk:"auth_required"`
	Encrypted     types.Bool   `tfsdk:"encrypted"`
}

func (r *fileSystemResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_filesystem"
}

func (r *fileSystemResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A filesystem: its group, capacity, mount authentication, and encryption.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description:   "Filesystem UID.",
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Description: "Filesystem name. Renaming updates in place.",
				Required:    true,
			},
			"group_name": schema.StringAttribute{
				Description:   "Name of the filesystem group this filesystem belongs to. Changing it replaces the filesystem.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"total_capacity": schema.Int64Attribute{
				Description: "Total capacity in bytes.",
				Required:    true,
			},
			"ssd_capacity": schema.Int64Attribute{
				Description:   "SSD capacity in bytes. Server default on create; removing this later keeps the current value rather than reverting, since the API has no way to ask for the default back.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"auth_required": schema.BoolAttribute{
				Description:   "Require an authenticated user to mount this filesystem. Only takes effect in the root organization. Server default on create; removing this later keeps the current value rather than reverting, since the API has no way to ask for the default back.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"encrypted": schema.BoolAttribute{
				Description: "Encrypt the filesystem. Changing it replaces the filesystem. Server default on create; removing this later keeps the current value rather than reverting, since the API has no way to ask for the default back.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.RequiresReplace(),
					boolplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *fileSystemResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.api = configuredAPI(req.ProviderData, &resp.Diagnostics)
}

func (r *fileSystemResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan fileSystemModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	out, err := r.api.CreateFileSystemWithResponse(ctx, wekaapi.CreateFileSystemJSONRequestBody{
		Name:          plan.Name.ValueString(),
		GroupName:     plan.GroupName.ValueString(),
		TotalCapacity: plan.TotalCapacity.ValueInt64(),
		SsdCapacity:   int64Ptr(plan.SSDCapacity),
		AuthRequired:  boolPtr(plan.AuthRequired),
		Encrypted:     boolPtr(plan.Encrypted),
	})
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err == nil && (out.JSON200 == nil || out.JSON200.Data == nil || deref(out.JSON200.Data.Uid) == "") {
		err = errors.New("create response has no uid")
	}
	var fs wekaapi.FileSystem
	if err == nil {
		// The fileSystem schema's own example is is_creating=true, is_ready=false,
		// status=CREATING, and create returns that object. Wait until it is ready
		// rather than storing a half-created filesystem. A response with neither
		// flag set is already finished; do not poll that.
		fs, err = r.waitReady(ctx, *out.JSON200.Data)
	}
	if err != nil {
		resp.Diagnostics.AddError("Creating filesystem", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, fileSystemFromAPI(fs))...)
}

func (r *fileSystemResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state fileSystemModel
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
		resp.Diagnostics.AddError("Reading filesystem", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *fileSystemResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state fileSystemModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body := wekaapi.UpdateFileSystemJSONRequestBody{
		TotalCapacity: int64Ptr(plan.TotalCapacity),
		SsdCapacity:   int64Ptr(plan.SSDCapacity),
		AuthRequired:  boolPtr(plan.AuthRequired),
	}
	if !plan.Name.Equal(state.Name) {
		body.NewName = plan.Name.ValueStringPointer()
	}
	out, err := r.api.UpdateFileSystemWithResponse(ctx, state.ID.ValueString(), body)
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	var m fileSystemModel
	if err == nil {
		m, err = r.get(ctx, state.ID.ValueString())
	}
	if err != nil {
		resp.Diagnostics.AddError("Updating filesystem", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *fileSystemResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state fileSystemModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	out, err := r.api.DeleteFileSystemWithResponse(ctx, state.ID.ValueString(), nil)
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil && !wekaapi.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting filesystem", err.Error())
	}
}

// ImportState accepts a UID or a name, so existing filesystems can be adopted
// by the name people already know.
func (r *fileSystemResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	out, err := r.api.GetFileSystemsWithResponse(ctx, forceFreshList())
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil {
		resp.Diagnostics.AddError("Importing filesystem", err.Error())
		return
	}
	if out.JSON200 != nil && out.JSON200.Data != nil {
		for _, fs := range *out.JSON200.Data {
			if deref(fs.Uid) == req.ID || deref(fs.Name) == req.ID {
				resp.Diagnostics.Append(resp.State.Set(ctx, fileSystemFromAPI(fs))...)
				return
			}
		}
	}
	resp.Diagnostics.AddError("Importing filesystem", fmt.Sprintf("no filesystem with UID or name %q", req.ID))
}

func (r *fileSystemResource) get(ctx context.Context, uid string) (fileSystemModel, error) {
	fs, err := r.getRaw(ctx, uid)
	if err != nil {
		return fileSystemModel{}, err
	}
	return fileSystemFromAPI(fs), nil
}

func (r *fileSystemResource) getRaw(ctx context.Context, uid string) (wekaapi.FileSystem, error) {
	out, err := r.api.GetFileSystemWithResponse(ctx, uid, forceFreshOne())
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil {
		return wekaapi.FileSystem{}, err
	}
	if out.JSON200 == nil || out.JSON200.Data == nil {
		return wekaapi.FileSystem{}, errors.New("get response has no data")
	}
	return *out.JSON200.Data, nil
}

// waitReady polls while the filesystem is still being created. Create returns
// the filesystem object immediately; the spec example has is_creating set and
// is_ready clear.
func (r *fileSystemResource) waitReady(ctx context.Context, fs wekaapi.FileSystem) (wekaapi.FileSystem, error) {
	if !fileSystemPending(fs) {
		return fs, nil
	}
	uid := deref(fs.Uid)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var ready wekaapi.FileSystem
	err := wekaapi.Poll(ctx, time.Second, func(ctx context.Context) (bool, error) {
		got, err := r.getRaw(ctx, uid)
		if err != nil {
			return false, err
		}
		if fileSystemPending(got) {
			return false, nil
		}
		ready = got
		return true, nil
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return wekaapi.FileSystem{}, fmt.Errorf("filesystem %s did not become ready within 2 minutes", uid)
		}
		return wekaapi.FileSystem{}, err
	}
	return ready, nil
}

func fileSystemPending(fs wekaapi.FileSystem) bool {
	if fs.IsCreating != nil && *fs.IsCreating {
		return true
	}
	if fs.IsReady != nil && !*fs.IsReady {
		return true
	}
	return false
}

func fileSystemFromAPI(fs wekaapi.FileSystem) fileSystemModel {
	// Create and update send total_capacity and ssd_capacity. The filesystem
	// object reports those as total_budget and ssd_budget (the spec uses the
	// same example value, 1073741824, for each pair). Encryption comes back
	// as is_encrypted.
	return fileSystemModel{
		ID:            types.StringValue(deref(fs.Uid)),
		Name:          types.StringValue(deref(fs.Name)),
		GroupName:     types.StringValue(deref(fs.GroupName)),
		TotalCapacity: int64Value(fs.TotalBudget),
		SSDCapacity:   int64Value(fs.SsdBudget),
		AuthRequired:  boolValue(fs.AuthRequired),
		Encrypted:     boolValue(fs.IsEncrypted),
	}
}

// forceFreshOne and forceFreshList ask the API to recompute capacities.
// Without it, a read just after create or update can report the previous
// total_budget / ssd_budget. The two parameter types are not interchangeable.
func forceFreshOne() *wekaapi.GetFileSystemParams {
	v := true
	return &wekaapi.GetFileSystemParams{ForceFresh: &v}
}

func forceFreshList() *wekaapi.GetFileSystemsParams {
	v := true
	return &wekaapi.GetFileSystemsParams{ForceFresh: &v}
}

func int64Ptr(v types.Int64) *int64 {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	n := v.ValueInt64()
	return &n
}

func int64Value(n *int64) types.Int64 {
	if n == nil {
		return types.Int64Null()
	}
	return types.Int64Value(*n)
}

func boolPtr(v types.Bool) *bool {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	b := v.ValueBool()
	return &b
}

func boolValue(b *bool) types.Bool {
	if b == nil {
		return types.BoolNull()
	}
	return types.BoolValue(*b)
}
