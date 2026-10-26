package provider

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
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
	_ resource.Resource                = (*objectStoreBucketResource)(nil)
	_ resource.ResourceWithConfigure   = (*objectStoreBucketResource)(nil)
	_ resource.ResourceWithImportState = (*objectStoreBucketResource)(nil)
)

func NewObjectStoreBucketResource() resource.Resource {
	return &objectStoreBucketResource{}
}

type objectStoreBucketResource struct {
	api *wekaapi.API
}

type objectStoreBucketModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	ObsName     types.String `tfsdk:"obs_name"`
	ObsSite     types.String `tfsdk:"obs_site"`
	ObsType     types.String `tfsdk:"obs_type"`
	Protocol    types.String `tfsdk:"protocol"`
	Hostname    types.String `tfsdk:"hostname"`
	Port        types.Int64  `tfsdk:"port"`
	Bucket      types.String `tfsdk:"bucket"`
	AuthMethod  types.String `tfsdk:"auth_method"`
	Region      types.String `tfsdk:"region"`
	AccessKeyID types.String `tfsdk:"access_key_id"`
	SecretKey   types.String `tfsdk:"secret_key"`
}

func (r *objectStoreBucketResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_object_store_bucket"
}

func (r *objectStoreBucketResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "An object store bucket, and how the cluster reaches it.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description:   "Object store bucket UID.",
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Description: "Object store bucket name. Renaming updates in place.",
				Required:    true,
			},
			"obs_name": schema.StringAttribute{
				Description: "Object store this bucket belongs to. Changing it updates in place.",
				Required:    true,
			},
			"obs_site": schema.StringAttribute{
				Description:   "Site of the bucket (local or remote). The update API has no field for this, so a change recreates the bucket. Server default on create; removing this later keeps the current value rather than reverting, since the API has no way to ask for the default back.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace(), stringplanmodifier.UseStateForUnknown()},
			},
			"obs_type": schema.StringAttribute{
				Description:   "Object store type. AWS when omitted. The update API has no field for this, so a change recreates the bucket. Removing this later keeps the current value rather than reverting, since the API has no way to ask for the default back.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace(), stringplanmodifier.UseStateForUnknown()},
				Validators: []validator.String{stringvalidator.OneOf(
					string(wekaapi.AWS),
					string(wekaapi.AZURE),
					string(wekaapi.HCP),
					string(wekaapi.OTHER),
				)},
			},
			"protocol": schema.StringAttribute{
				Description:   "Protocol used to reach the object store. HTTP when omitted. Removing this later keeps the current value rather than reverting, since the API has no way to ask for the default back.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators: []validator.String{stringvalidator.OneOf(
					string(wekaapi.CreateObsBucketJSONBodyProtocolHTTP),
					string(wekaapi.CreateObsBucketJSONBodyProtocolHTTPS),
					string(wekaapi.CreateObsBucketJSONBodyProtocolHTTPSUNVERIFIED),
				)},
			},
			"hostname": schema.StringAttribute{
				Description:   "Hostname or IP of the object store endpoint. Removing this later keeps the current value rather than reverting, since the API has no way to ask for the default back.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"port": schema.Int64Attribute{
				Description:   "Port of the object store endpoint. Removing this later keeps the current value rather than reverting, since the API has no way to ask for the default back.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"bucket": schema.StringAttribute{
				Description:   "Bucket name in the object store. Removing this later keeps the current value rather than reverting, since the API has no way to ask for the default back.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"auth_method": schema.StringAttribute{
				Description:   "Authentication method: None, AWSSignature2, or AWSSignature4. Removing this later keeps the current value rather than reverting, since the API has no way to ask for the default back.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators: []validator.String{stringvalidator.OneOf(
					string(wekaapi.CreateObsBucketJSONBodyAuthMethodNone),
					string(wekaapi.CreateObsBucketJSONBodyAuthMethodAWSSignature2),
					string(wekaapi.CreateObsBucketJSONBodyAuthMethodAWSSignature4),
				)},
			},
			"region": schema.StringAttribute{
				Description:   "Region the bucket is assigned to. Usually empty. Removing this later keeps the current value rather than reverting, since the API has no way to ask for the default back.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"access_key_id": schema.StringAttribute{
				Description:   "Access key ID for AWS Signature authentication. Removing this later keeps the current value rather than reverting, since the API has no way to ask for the default back.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"secret_key": schema.StringAttribute{
				Description: "Secret key for AWS Signature authentication. When a read omits this, Terraform keeps the configured value and does not plan a change.",
				Optional:    true,
				Sensitive:   true,
			},
		},
	}
}

func (r *objectStoreBucketResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.api = configuredAPI(req.ProviderData, &resp.Diagnostics)
}

func (r *objectStoreBucketResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan objectStoreBucketModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	obsName := plan.ObsName.ValueString()
	out, err := r.api.CreateObsBucketWithResponse(ctx, wekaapi.CreateObsBucketJSONRequestBody{
		Name:        plan.Name.ValueString(),
		ObsName:     &obsName,
		ObsSite:     optString(plan.ObsSite),
		ObsType:     enumPtr[wekaapi.CreateObsBucketJSONBodyObsType](plan.ObsType),
		Protocol:    enumPtr[wekaapi.CreateObsBucketJSONBodyProtocol](plan.Protocol),
		Hostname:    optString(plan.Hostname),
		Port:        portPtr(plan.Port),
		Bucket:      optString(plan.Bucket),
		AuthMethod:  enumPtr[wekaapi.CreateObsBucketJSONBodyAuthMethod](plan.AuthMethod),
		Region:      optString(plan.Region),
		AccessKeyId: optString(plan.AccessKeyID),
		SecretKey:   optString(plan.SecretKey),
	})
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err == nil && (out.JSON200 == nil || out.JSON200.Data == nil || out.JSON200.Data.Uid == nil) {
		err = errors.New("create response has no uid")
	}
	if err != nil {
		resp.Diagnostics.AddError("Creating object store bucket", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, objectStoreBucketFromAPI(*out.JSON200.Data, plan.SecretKey))...)
}

func (r *objectStoreBucketResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state objectStoreBucketModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	m, err := r.get(ctx, state.ID.ValueString(), state.SecretKey)
	if wekaapi.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading object store bucket", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *objectStoreBucketResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state objectStoreBucketModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body := wekaapi.UpdateObsBucketJSONRequestBody{
		Protocol:    enumPtr[wekaapi.UpdateObsBucketJSONBodyProtocol](plan.Protocol),
		Hostname:    optString(plan.Hostname),
		Port:        portPtr(plan.Port),
		Bucket:      optString(plan.Bucket),
		AuthMethod:  enumPtr[wekaapi.UpdateObsBucketJSONBodyAuthMethod](plan.AuthMethod),
		Region:      optString(plan.Region),
		AccessKeyId: optString(plan.AccessKeyID),
		SecretKey:   optString(plan.SecretKey),
	}
	if !plan.Name.Equal(state.Name) {
		name := plan.Name.ValueString()
		body.NewName = &name
	}
	if !plan.ObsName.Equal(state.ObsName) {
		obsName := plan.ObsName.ValueString()
		body.NewObsName = &obsName
	}
	out, err := r.api.UpdateObsBucketWithResponse(ctx, state.ID.ValueString(), body)
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	var m objectStoreBucketModel
	if err == nil {
		m, err = r.get(ctx, state.ID.ValueString(), plan.SecretKey)
	}
	if err != nil {
		resp.Diagnostics.AddError("Updating object store bucket", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *objectStoreBucketResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state objectStoreBucketModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	out, err := r.api.DeleteObsBucketWithResponse(ctx, state.ID.ValueString())
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil && !wekaapi.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting object store bucket", err.Error())
	}
}

// ImportState accepts a UID or a name, so an existing bucket can be adopted by
// the name people already know.
func (r *objectStoreBucketResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	out, err := r.api.GetAllObsBucketsWithResponse(ctx)
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil {
		resp.Diagnostics.AddError("Importing object store bucket", err.Error())
		return
	}
	if out.JSON200 != nil && out.JSON200.Data != nil {
		for _, b := range *out.JSON200.Data {
			if deref(b.Uid) == req.ID || deref(b.Name) == req.ID {
				resp.Diagnostics.Append(resp.State.Set(ctx, objectStoreBucketFromAPI(b, types.StringNull()))...)
				return
			}
		}
	}
	resp.Diagnostics.AddError("Importing object store bucket", fmt.Sprintf("no object store bucket with UID or name %q", req.ID))
}

func (r *objectStoreBucketResource) get(ctx context.Context, uid string, secret types.String) (objectStoreBucketModel, error) {
	out, err := r.api.GetObsBucketsWithResponse(ctx, uid)
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil {
		return objectStoreBucketModel{}, err
	}
	if out.JSON200 == nil || out.JSON200.Data == nil {
		return objectStoreBucketModel{}, errors.New("get response has no data")
	}
	return objectStoreBucketFromAPI(*out.JSON200.Data, secret), nil
}

func objectStoreBucketFromAPI(b wekaapi.ObjectStoreBucket, secret types.String) objectStoreBucketModel {
	return objectStoreBucketModel{
		ID:          types.StringValue(deref(b.Uid)),
		Name:        types.StringValue(deref(b.Name)),
		ObsName:     types.StringValue(deref(b.ObsName)),
		ObsSite:     types.StringPointerValue(b.ObsSite),
		ObsType:     types.StringPointerValue(b.ObsType),
		Protocol:    types.StringPointerValue(b.Protocol),
		Hostname:    types.StringPointerValue(b.Hostname),
		Port:        portValue(b.Port),
		Bucket:      types.StringPointerValue(b.Bucket),
		AuthMethod:  types.StringPointerValue(b.AuthMethod),
		Region:      types.StringPointerValue(b.Region),
		AccessKeyID: types.StringPointerValue(b.AccessKeyId),
		SecretKey:   secretKept(b.SecretKey, secret),
	}
}

// optString omits null and unknown values. ValueStringPointer returns a pointer
// to "" for unknown, which would clobber a server default on create.
func optString(v types.String) *string {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	s := v.ValueString()
	return &s
}

func enumPtr[T ~string](v types.String) *T {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	e := T(v.ValueString())
	return &e
}

func portPtr(v types.Int64) *float32 {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	f := float32(v.ValueInt64())
	return &f
}

func portValue(f *float32) types.Int64 {
	if f == nil {
		return types.Int64Null()
	}
	return types.Int64Value(int64(math.Round(float64(*f))))
}

// secretKept keeps the configured secret when GET omits it. The response
// schema's example is a mask of asterisks; that is not the key, and writing it
// back on a later update would replace the real one. A value the API actually
// returns is kept, so drift is still visible.
func secretKept(api *string, configured types.String) types.String {
	if api != nil && !secretOmitted(*api) {
		return types.StringValue(*api)
	}
	if configured.IsNull() || configured.IsUnknown() {
		return types.StringNull()
	}
	return configured
}

func secretOmitted(s string) bool {
	return s == "" || strings.Trim(s, "*") == ""
}
