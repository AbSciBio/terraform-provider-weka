package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/AbSciBio/terraform-provider-weka/internal/wekaapi"
)

var (
	_ resource.Resource                = (*snapshotPolicyResource)(nil)
	_ resource.ResourceWithConfigure   = (*snapshotPolicyResource)(nil)
	_ resource.ResourceWithImportState = (*snapshotPolicyResource)(nil)
)

func NewSnapshotPolicyResource() resource.Resource {
	return &snapshotPolicyResource{}
}

type snapshotPolicyResource struct {
	api *wekaapi.API
}

// scheduleModel covers daily (and, structurally, weekly): a time of day, a
// day filter, retention, and where it uploads.
type scheduleModel struct {
	Enable    types.Bool   `tfsdk:"enable"`
	Time      types.String `tfsdk:"time"`
	Days      types.String `tfsdk:"days"`
	Retention types.Int64  `tfsdk:"retention"`
	Upload    types.String `tfsdk:"upload"`
}

// hourlyModel runs on an hour list instead of a single time.
type hourlyModel struct {
	Enable       types.Bool   `tfsdk:"enable"`
	Hours        types.String `tfsdk:"hours"`
	Days         types.String `tfsdk:"days"`
	MinuteOffset types.Int64  `tfsdk:"minute_offset"`
	Retention    types.Int64  `tfsdk:"retention"`
	Upload       types.String `tfsdk:"upload"`
}

type snapshotPolicyModel struct {
	ID          types.String   `tfsdk:"id"`
	Name        types.String   `tfsdk:"name"`
	Description types.String   `tfsdk:"description"`
	Enable      types.Bool     `tfsdk:"enable"`
	Filesystems []types.String `tfsdk:"filesystems"`
	Daily       *scheduleModel `tfsdk:"daily"`
	Hourly      *hourlyModel   `tfsdk:"hourly"`
}

func (r *snapshotPolicyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_snapshot_policy"
}

func (r *snapshotPolicyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A snapshot schedule attached to one or more filesystems. The cluster's policy_params always carries all five schedule kinds (daily, hourly, weekly, monthly, periodic); this resource exposes the two actually used in practice, daily and hourly, and sends the other three disabled. upload is \"none\", \"local\", or \"remote\" and must be \"remote\" for a filesystem whose only object store attachment is a remote (snapshot) bucket.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description:   "Snapshot policy UID.",
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Description: "Policy name. Max 12 characters. Renaming updates in place.",
				Required:    true,
			},
			"description": schema.StringAttribute{
				Description: "Free-text description.",
				Optional:    true,
				Computed:    true,
			},
			"enable": schema.BoolAttribute{
				Description: "Whether the whole policy is active.",
				Optional:    true,
				Computed:    true,
			},
			"filesystems": schema.SetAttribute{
				Description: "UIDs of the filesystems this policy runs against. A set: the API does not promise a stable order.",
				ElementType: types.StringType,
				Required:    true,
			},
			"daily": scheduleAttribute("Runs once a day at a fixed time."),
			"hourly": schema.SingleNestedAttribute{
				Description: "Runs on an hour list within selected days.",
				Optional:    true,
				Attributes: map[string]schema.Attribute{
					"enable":        schema.BoolAttribute{Required: true},
					"hours":         schema.StringAttribute{Description: "Comma-separated hours, e.g. \"09, 10, 11\", or \"all\".", Required: true},
					"days":          schema.StringAttribute{Description: "Comma-separated weekday names, e.g. \"monday, tuesday\", or \"all\".", Required: true},
					"minute_offset": schema.Int64Attribute{Required: true},
					"retention":     schema.Int64Attribute{Required: true},
					"upload":        schema.StringAttribute{Required: true},
				},
			},
		},
	}
}

func scheduleAttribute(desc string) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Description: desc,
		Optional:    true,
		Attributes: map[string]schema.Attribute{
			"enable":    schema.BoolAttribute{Required: true},
			"time":      schema.StringAttribute{Description: "HH:MM.", Required: true},
			"days":      schema.StringAttribute{Description: "Comma-separated weekday names, e.g. \"monday, tuesday\", or \"all\".", Required: true},
			"retention": schema.Int64Attribute{Required: true},
			"upload":    schema.StringAttribute{Required: true},
		},
	}
}

func (r *snapshotPolicyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.api = configuredAPI(req.ProviderData, &resp.Diagnostics)
}

// Sentinel values this provider sends for a daily/hourly block the config
// omits. The API always echoes back a full five-kind template regardless of
// what was sent, so Read uses these same values (deref'd) to tell "config
// left this out" apart from "config explicitly set it", keeping the
// attribute a plain Optional object instead of Optional+Computed. A
// Computed nested object cannot hold an unknown value on create (the
// framework rejects *T there), so this sidesteps that instead of fighting it.
const (
	disabledDays   = "all"
	disabledTime   = "00:00"
	disabledHours  = "00"
	disabledUpload = "none"
)

// policyParams builds the full five-kind template the API requires on every
// create and update. weekly/monthly/periodic are always sent disabled; this
// resource does not expose them.
func policyParams(daily *scheduleModel, hourly *hourlyModel) wekaapi.SnapshotPolicyTemplate {
	t := wekaapi.SnapshotPolicyTemplate{}
	f := false
	t.Weekly = &struct {
		Days      *string  `json:"days,omitempty"`
		Enable    *bool    `json:"enable,omitempty"`
		Retention *float32 `json:"retention,omitempty"`
		Time      *string  `json:"time,omitempty"`
		Upload    *string  `json:"upload,omitempty"`
	}{Enable: &f}
	t.Monthly = &struct {
		Days      *string  `json:"days,omitempty"`
		Enable    *bool    `json:"enable,omitempty"`
		Months    *string  `json:"months,omitempty"`
		Retention *float32 `json:"retention,omitempty"`
		Time      *string  `json:"time,omitempty"`
		Upload    *string  `json:"upload,omitempty"`
	}{Enable: &f}
	t.Periodic = &struct {
		Days      *string  `json:"days,omitempty"`
		Enable    *bool    `json:"enable,omitempty"`
		EndTime   *string  `json:"end_time,omitempty"`
		Interval  *float32 `json:"interval,omitempty"`
		Retention *float32 `json:"retention,omitempty"`
		StartTime *string  `json:"start_time,omitempty"`
		Upload    *string  `json:"upload,omitempty"`
	}{Enable: &f}

	if daily != nil {
		retention := float32(daily.Retention.ValueInt64())
		t.Daily = &struct {
			Days      *string  `json:"days,omitempty"`
			Enable    *bool    `json:"enable,omitempty"`
			Retention *float32 `json:"retention,omitempty"`
			Time      *string  `json:"time,omitempty"`
			Upload    *string  `json:"upload,omitempty"`
		}{
			Enable:    daily.Enable.ValueBoolPointer(),
			Time:      daily.Time.ValueStringPointer(),
			Days:      daily.Days.ValueStringPointer(),
			Retention: &retention,
			Upload:    daily.Upload.ValueStringPointer(),
		}
	} else {
		days, tm, upload := disabledDays, disabledTime, disabledUpload
		zero := float32(0)
		t.Daily = &struct {
			Days      *string  `json:"days,omitempty"`
			Enable    *bool    `json:"enable,omitempty"`
			Retention *float32 `json:"retention,omitempty"`
			Time      *string  `json:"time,omitempty"`
			Upload    *string  `json:"upload,omitempty"`
		}{Enable: &f, Days: &days, Time: &tm, Retention: &zero, Upload: &upload}
	}

	if hourly != nil {
		retention := float32(hourly.Retention.ValueInt64())
		offset := float32(hourly.MinuteOffset.ValueInt64())
		t.Hourly = &struct {
			Days         *string  `json:"days,omitempty"`
			Enable       *bool    `json:"enable,omitempty"`
			Hours        *string  `json:"hours,omitempty"`
			MinuteOffset *float32 `json:"minuteOffset,omitempty"`
			Retention    *float32 `json:"retention,omitempty"`
			Upload       *string  `json:"upload,omitempty"`
		}{
			Enable:       hourly.Enable.ValueBoolPointer(),
			Hours:        hourly.Hours.ValueStringPointer(),
			Days:         hourly.Days.ValueStringPointer(),
			MinuteOffset: &offset,
			Retention:    &retention,
			Upload:       hourly.Upload.ValueStringPointer(),
		}
	} else {
		days, hours, upload := disabledDays, disabledHours, disabledUpload
		zero := float32(0)
		t.Hourly = &struct {
			Days         *string  `json:"days,omitempty"`
			Enable       *bool    `json:"enable,omitempty"`
			Hours        *string  `json:"hours,omitempty"`
			MinuteOffset *float32 `json:"minuteOffset,omitempty"`
			Retention    *float32 `json:"retention,omitempty"`
			Upload       *string  `json:"upload,omitempty"`
		}{Enable: &f, Days: &days, Hours: &hours, MinuteOffset: &zero, Retention: &zero, Upload: &upload}
	}
	return t
}

func (r *snapshotPolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan snapshotPolicyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	enable := plan.Enable.ValueBoolPointer()
	if plan.Enable.IsNull() || plan.Enable.IsUnknown() {
		t := true
		enable = &t
	}
	desc := plan.Description.ValueStringPointer()
	params := policyParams(plan.Daily, plan.Hourly)
	out, err := r.api.CreateSnapshotPolicyWithResponse(ctx, wekaapi.CreateSnapshotPolicyJSONRequestBody{
		Name:         plan.Name.ValueStringPointer(),
		Description:  desc,
		Enable:       enable,
		PolicyParams: &params,
	})
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err == nil && (out.JSON200 == nil || out.JSON200.Data == nil || deref(out.JSON200.Data.Uid) == "") {
		err = errors.New("create response has no uid")
	}
	if err != nil {
		resp.Diagnostics.AddError("Creating snapshot policy", err.Error())
		return
	}
	uid := *out.JSON200.Data.Uid

	if err := r.setFilesystems(ctx, uid, nil, stringValues(plan.Filesystems)); err != nil {
		resp.Diagnostics.AddError("Attaching filesystems to snapshot policy", err.Error())
		return
	}

	m, err := r.get(ctx, uid)
	if err != nil {
		resp.Diagnostics.AddError("Reading snapshot policy after create", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *snapshotPolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state snapshotPolicyModel
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
		resp.Diagnostics.AddError("Reading snapshot policy", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *snapshotPolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state snapshotPolicyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	uid := state.ID.ValueString()
	body := wekaapi.UpdateSnapshotPolicyJSONRequestBody{
		Description: plan.Description.ValueStringPointer(),
		Enable:      plan.Enable.ValueBoolPointer(),
	}
	if !plan.Name.Equal(state.Name) {
		body.NewName = plan.Name.ValueStringPointer()
	}
	params := policyParams(plan.Daily, plan.Hourly)
	body.PolicyParams = &params
	out, err := r.api.UpdateSnapshotPolicyWithResponse(ctx, uid, body)
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil {
		resp.Diagnostics.AddError("Updating snapshot policy", err.Error())
		return
	}

	if err := r.setFilesystems(ctx, uid, stringValues(state.Filesystems), stringValues(plan.Filesystems)); err != nil {
		resp.Diagnostics.AddError("Updating snapshot policy filesystems", err.Error())
		return
	}

	m, err := r.get(ctx, uid)
	if err != nil {
		resp.Diagnostics.AddError("Reading snapshot policy after update", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *snapshotPolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state snapshotPolicyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	uid := state.ID.ValueString()
	// The cluster refuses to delete a policy with any filesystem still
	// attached; detach everything first.
	if err := r.setFilesystems(ctx, uid, stringValues(state.Filesystems), nil); err != nil && !wekaapi.IsNotFound(err) {
		resp.Diagnostics.AddError("Detaching filesystems from snapshot policy", err.Error())
		return
	}

	out, err := r.api.DeleteSnapshotPolicyWithResponse(ctx, uid)
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil && !wekaapi.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting snapshot policy", err.Error())
	}
}

// ImportState accepts a UID or a name, same convention as the other resources.
func (r *snapshotPolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	out, err := r.api.ListSnapPoliciesWithResponse(ctx)
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil {
		resp.Diagnostics.AddError("Importing snapshot policy", err.Error())
		return
	}
	if out.JSON200 != nil && out.JSON200.Data != nil {
		for _, p := range *out.JSON200.Data {
			if deref(p.Uid) == req.ID || deref(p.Name) == req.ID {
				m, err := r.get(ctx, deref(p.Uid))
				if err != nil {
					resp.Diagnostics.AddError("Importing snapshot policy", err.Error())
					return
				}
				resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
				return
			}
		}
	}
	resp.Diagnostics.AddError("Importing snapshot policy", fmt.Sprintf("no snapshot policy with UID or name %q", req.ID))
}

// setFilesystems diffs want against have and calls attach/detach for the
// difference. Both lists are filesystem UIDs.
func (r *snapshotPolicyResource) setFilesystems(ctx context.Context, uid string, have, want []string) error {
	haveSet, wantSet := map[string]bool{}, map[string]bool{}
	for _, v := range have {
		haveSet[v] = true
	}
	for _, v := range want {
		wantSet[v] = true
	}
	var toAttach, toDetach []string
	for _, v := range want {
		if !haveSet[v] {
			toAttach = append(toAttach, v)
		}
	}
	for _, v := range have {
		if !wantSet[v] {
			toDetach = append(toDetach, v)
		}
	}
	if len(toDetach) > 0 {
		out, err := r.api.DetachFsFromSnapshotPolicyWithResponse(ctx, uid, nil, wekaapi.DetachFsFromSnapshotPolicyJSONRequestBody{Filesystems: &toDetach})
		if err == nil {
			err = wekaapi.Check(out.StatusCode(), out.Body)
		}
		if err != nil {
			return fmt.Errorf("detaching filesystems: %w", err)
		}
	}
	if len(toAttach) > 0 {
		out, err := r.api.AttachFsToSnapshotPolicyWithResponse(ctx, uid, wekaapi.AttachFsToSnapshotPolicyJSONRequestBody{Filesystems: &toAttach})
		if err == nil {
			err = wekaapi.Check(out.StatusCode(), out.Body)
		}
		if err != nil {
			return fmt.Errorf("attaching filesystems: %w", err)
		}
	}
	return nil
}

// snapshotPolicyWire is the shape actually returned by GET /snapshotPolicy/{uid}.
// The generated SnapshotPolicyInfo.Enable field is tagged "enable", but the
// live API's top-level key is "enabled"; decode that one field ourselves
// rather than trust the generated struct. The nested schedule "enable" keys
// are correct and reused as-is. filesystems comes back as names, not UIDs.
type snapshotPolicyWire struct {
	Name        *string  `json:"name"`
	Description *string  `json:"description"`
	Enabled     *bool    `json:"enabled"`
	Filesystems []string `json:"filesystems"`
}

func (r *snapshotPolicyResource) get(ctx context.Context, uid string) (snapshotPolicyModel, error) {
	out, err := r.api.ShowSnapshotPolicyWithResponse(ctx, uid)
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil {
		return snapshotPolicyModel{}, err
	}
	if out.JSON200 == nil || out.JSON200.Data == nil {
		return snapshotPolicyModel{}, errors.New("get response has no data")
	}

	var wire snapshotPolicyWire
	if err := json.Unmarshal(out.Body, &struct {
		Data *snapshotPolicyWire `json:"data"`
	}{Data: &wire}); err != nil {
		return snapshotPolicyModel{}, fmt.Errorf("decoding snapshot policy: %w", err)
	}
	info := *out.JSON200.Data

	fsUIDs, err := r.filesystemUIDs(ctx, wire.Filesystems)
	if err != nil {
		return snapshotPolicyModel{}, err
	}

	m := snapshotPolicyModel{
		ID:          types.StringValue(uid),
		Name:        types.StringValue(deref(wire.Name)),
		Description: types.StringValue(deref(wire.Description)),
		Enable:      boolValue(wire.Enabled),
		Filesystems: fsUIDs,
	}
	if info.Daily != nil && !isDisabledDaily(info.Daily) {
		m.Daily = &scheduleModel{
			Enable:    boolValue(info.Daily.Enable),
			Time:      types.StringValue(deref(info.Daily.Time)),
			Days:      types.StringValue(deref(info.Daily.Days)),
			Retention: int64FromFloat32(info.Daily.Retention),
			Upload:    types.StringValue(deref(info.Daily.Upload)),
		}
	}
	if info.Hourly != nil && !isDisabledHourly(info.Hourly) {
		m.Hourly = &hourlyModel{
			Enable:       boolValue(info.Hourly.Enable),
			Hours:        types.StringValue(deref(info.Hourly.Hours)),
			Days:         types.StringValue(deref(info.Hourly.Days)),
			MinuteOffset: int64FromFloat32(info.Hourly.MinuteOffset),
			Retention:    int64FromFloat32(info.Hourly.Retention),
			Upload:       types.StringValue(deref(info.Hourly.Upload)),
		}
	}
	return m, nil
}

// filesystemUIDs maps the filesystem names the API returns back to UIDs, so
// state stays consistent with the UIDs this resource is configured with. The
// result is sorted: filesystems is a list, and nothing guarantees the API
// returns it in the same order across calls.
func (r *snapshotPolicyResource) filesystemUIDs(ctx context.Context, names []string) ([]types.String, error) {
	if len(names) == 0 {
		return nil, nil
	}
	out, err := r.api.GetFileSystemsWithResponse(ctx, nil)
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil {
		return nil, fmt.Errorf("listing filesystems: %w", err)
	}
	byName := map[string]string{}
	if out.JSON200 != nil && out.JSON200.Data != nil {
		for _, fs := range *out.JSON200.Data {
			byName[deref(fs.Name)] = deref(fs.Uid)
		}
	}
	uids := make([]types.String, 0, len(names))
	for _, n := range names {
		if uid, ok := byName[n]; ok {
			uids = append(uids, types.StringValue(uid))
		} else {
			uids = append(uids, types.StringValue(n))
		}
	}
	sort.Slice(uids, func(i, j int) bool { return uids[i].ValueString() < uids[j].ValueString() })
	return uids, nil
}

// isDisabledDaily/isDisabledHourly report whether the API's schedule exactly
// matches the sentinel this provider sends for an omitted block. See the
// disabledDays etc. constants above for why this check exists.
func isDisabledDaily(d *struct {
	Days      *string  `json:"days,omitempty"`
	Enable    *bool    `json:"enable,omitempty"`
	Retention *float32 `json:"retention,omitempty"`
	Time      *string  `json:"time,omitempty"`
	Upload    *string  `json:"upload,omitempty"`
}) bool {
	return !deref(d.Enable) && deref(d.Days) == disabledDays && deref(d.Time) == disabledTime &&
		deref(d.Retention) == 0 && deref(d.Upload) == disabledUpload
}

func isDisabledHourly(h *struct {
	Days         *string  `json:"days,omitempty"`
	Enable       *bool    `json:"enable,omitempty"`
	Hours        *string  `json:"hours,omitempty"`
	MinuteOffset *float32 `json:"minuteOffset,omitempty"`
	Retention    *float32 `json:"retention,omitempty"`
	Upload       *string  `json:"upload,omitempty"`
}) bool {
	return !deref(h.Enable) && deref(h.Days) == disabledDays && deref(h.Hours) == disabledHours &&
		deref(h.MinuteOffset) == 0 && deref(h.Retention) == 0 && deref(h.Upload) == disabledUpload
}

func stringValues(vs []types.String) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.ValueString())
	}
	return out
}

func int64FromFloat32(f *float32) types.Int64 {
	if f == nil {
		return types.Int64Null()
	}
	return types.Int64Value(int64(*f))
}
