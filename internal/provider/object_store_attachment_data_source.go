package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"

	"github.com/AbSciBio/terraform-provider-weka/internal/wekaapi"
)

var (
	_ datasource.DataSource              = (*objectStoreAttachmentDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*objectStoreAttachmentDataSource)(nil)
)

func NewObjectStoreAttachmentDataSource() datasource.DataSource {
	return &objectStoreAttachmentDataSource{}
}

type objectStoreAttachmentDataSource struct {
	api *wekaapi.API
}

func (d *objectStoreAttachmentDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_object_store_attachment"
}

func (d *objectStoreAttachmentDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Look up an existing object store bucket attachment by its \"<filesystem_id>/<object_store_bucket_id>\" id.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "<filesystem_id>/<object_store_bucket_id>.",
				Required:    true,
			},
			"filesystem_id": schema.StringAttribute{
				Description: "UID of the filesystem the bucket is attached to.",
				Computed:    true,
			},
			"object_store_bucket_id": schema.StringAttribute{
				Description: "UID of the attached weka_object_store_bucket.",
				Computed:    true,
			},
			"mode": schema.StringAttribute{
				Description: "Attachment mode reported by the cluster: WRITABLE for a local (tiering) bucket, REMOTE for a remote (snapshot) bucket.",
				Computed:    true,
			},
		},
	}
}

func (d *objectStoreAttachmentDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.api = configuredAPI(req.ProviderData, &resp.Diagnostics)
}

func (d *objectStoreAttachmentDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data objectStoreAttachmentModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fsID, bucketID, ok := splitAttachmentID(data.ID.ValueString())
	if !ok {
		resp.Diagnostics.AddError("Reading object store attachment",
			fmt.Sprintf("expected \"<filesystem_id>/<object_store_bucket_id>\", got %q", data.ID.ValueString()))
		return
	}

	m, err := (&objectStoreAttachmentResource{api: d.api}).get(ctx, fsID, bucketID)
	if err != nil {
		resp.Diagnostics.AddError("Reading object store attachment", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}
