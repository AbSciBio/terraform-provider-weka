package provider

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/AbSciBio/terraform-provider-weka/internal/wekaapi"
)

var (
	_ resource.Resource                = (*objectStoreAttachmentResource)(nil)
	_ resource.ResourceWithConfigure   = (*objectStoreAttachmentResource)(nil)
	_ resource.ResourceWithImportState = (*objectStoreAttachmentResource)(nil)
)

func NewObjectStoreAttachmentResource() resource.Resource {
	return &objectStoreAttachmentResource{}
}

type objectStoreAttachmentResource struct {
	api *wekaapi.API
}

type objectStoreAttachmentModel struct {
	ID                  types.String `tfsdk:"id"`
	FilesystemID        types.String `tfsdk:"filesystem_id"`
	ObjectStoreBucketID types.String `tfsdk:"object_store_bucket_id"`
	Mode                types.String `tfsdk:"mode"`
}

func (r *objectStoreAttachmentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_object_store_attachment"
}

func (r *objectStoreAttachmentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Attaches an object store bucket to a filesystem for tiering (bucket's obs_site = local) or snapshots (obs_site = remote). The server decides the resulting mode from the bucket's own site; it is not a request parameter. Destroy detaches the bucket and waits until the cluster reports it gone. Detaching the only local bucket copies that filesystem's data back onto SSD.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description:   "<filesystem_id>/<object_store_bucket_id>.",
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"filesystem_id": schema.StringAttribute{
				Description:   "UID of the filesystem to attach the bucket to.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"object_store_bucket_id": schema.StringAttribute{
				Description:   "UID of the weka_object_store_bucket to attach.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"mode": schema.StringAttribute{
				Description: "Resulting attachment mode reported by the cluster: WRITABLE for a local (tiering) bucket, REMOTE for a remote (snapshot) bucket.",
				Computed:    true,
			},
		},
	}
}

func (r *objectStoreAttachmentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.api = configuredAPI(req.ProviderData, &resp.Diagnostics)
}

func (r *objectStoreAttachmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan objectStoreAttachmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fsID, bucketID := plan.FilesystemID.ValueString(), plan.ObjectStoreBucketID.ValueString()

	bucket, err := r.api.GetObsBucketsWithResponse(ctx, bucketID)
	if err == nil {
		err = wekaapi.Check(bucket.StatusCode(), bucket.Body)
	}
	if err == nil && (bucket.JSON200 == nil || bucket.JSON200.Data == nil || bucket.JSON200.Data.Name == nil) {
		err = errors.New("object store bucket has no name")
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading object store bucket to attach", err.Error())
		return
	}

	out, err := r.api.AttachObsBucketToFsWithResponse(ctx, fsID, wekaapi.AttachObsBucketToFsJSONRequestBody{
		ObsName: *bucket.JSON200.Data.Name,
	})
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil {
		resp.Diagnostics.AddError("Attaching object store bucket to filesystem", err.Error())
		return
	}

	m, err := r.get(ctx, fsID, bucketID)
	if err != nil {
		resp.Diagnostics.AddError("Reading attachment after create", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *objectStoreAttachmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state objectStoreAttachmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	m, err := r.get(ctx, state.FilesystemID.ValueString(), state.ObjectStoreBucketID.ValueString())
	if wekaapi.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Reading object store attachment", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

// Update never runs: both inputs are RequiresReplace and mode is Computed.
func (r *objectStoreAttachmentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan objectStoreAttachmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	m, err := r.get(ctx, plan.FilesystemID.ValueString(), plan.ObjectStoreBucketID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Reading object store attachment", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *objectStoreAttachmentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state objectStoreAttachmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fsID, bucketID := state.FilesystemID.ValueString(), state.ObjectStoreBucketID.ValueString()
	out, err := r.api.DetachObsBucketFromFSWithResponse(ctx, fsID, bucketID)
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil && !wekaapi.IsNotFound(err) {
		resp.Diagnostics.AddError("Detaching object store bucket from filesystem", err.Error())
		return
	}
	// Detach returns a task id. Wait until the bucket is gone from the
	// filesystem, same as filesystem delete. Two minutes matches that wait;
	// a detach that copies data back to SSD can outlive it.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	err = wekaapi.Poll(ctx, time.Second, func(ctx context.Context) (bool, error) {
		_, err := r.get(ctx, fsID, bucketID)
		if wekaapi.IsNotFound(err) {
			return true, nil
		}
		return false, err
	})
	if err != nil {
		resp.Diagnostics.AddError("Detaching object store bucket from filesystem", err.Error())
	}
}

// ImportState takes "<filesystem_id>/<object_store_bucket_id>", the only
// identifiers the relationship has; there is no name to adopt by.
func (r *objectStoreAttachmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	fsID, bucketID, ok := splitAttachmentID(req.ID)
	if !ok {
		resp.Diagnostics.AddError("Importing object store attachment",
			fmt.Sprintf("expected \"<filesystem_id>/<object_store_bucket_id>\", got %q", req.ID))
		return
	}
	m, err := r.get(ctx, fsID, bucketID)
	if err != nil {
		resp.Diagnostics.AddError("Importing object store attachment", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

// get reads the attachment via the filesystem's embedded obs_buckets list;
// there is no standalone GET for a single attachment.
func (r *objectStoreAttachmentResource) get(ctx context.Context, fsID, bucketID string) (objectStoreAttachmentModel, error) {
	out, err := r.api.GetFileSystemWithResponse(ctx, fsID, nil)
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil {
		return objectStoreAttachmentModel{}, err
	}
	if out.JSON200 == nil || out.JSON200.Data == nil || out.JSON200.Data.ObsBuckets == nil {
		return objectStoreAttachmentModel{}, &wekaapi.APIError{Status: 404, Message: "filesystem has no object store buckets attached"}
	}
	for _, b := range *out.JSON200.Data.ObsBuckets {
		if deref(b.Uid) == bucketID {
			return objectStoreAttachmentModel{
				ID:                  types.StringValue(fsID + "/" + bucketID),
				FilesystemID:        types.StringValue(fsID),
				ObjectStoreBucketID: types.StringValue(bucketID),
				Mode:                types.StringPointerValue(b.Mode),
			}, nil
		}
	}
	return objectStoreAttachmentModel{}, &wekaapi.APIError{Status: 404, Message: fmt.Sprintf("object store bucket %q is not attached to filesystem %q", bucketID, fsID)}
}

func splitAttachmentID(id string) (fsID, bucketID string, ok bool) {
	for i := len(id) - 1; i > 0; i-- {
		if id[i] == '/' {
			return id[:i], id[i+1:], true
		}
	}
	return "", "", false
}
