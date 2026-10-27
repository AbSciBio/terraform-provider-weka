package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
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
	_ resource.Resource                = (*userResource)(nil)
	_ resource.ResourceWithConfigure   = (*userResource)(nil)
	_ resource.ResourceWithImportState = (*userResource)(nil)
)

func NewUserResource() resource.Resource {
	return &userResource{}
}

type userResource struct {
	api *wekaapi.API
}

type userModel struct {
	ID       types.String `tfsdk:"id"`
	Username types.String `tfsdk:"username"`
	Password types.String `tfsdk:"password"`
	Role     types.String `tfsdk:"role"`
	PosixUID types.Int64  `tfsdk:"posix_uid"`
	PosixGID types.Int64  `tfsdk:"posix_gid"`
}

func (r *userResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user"
}

func (r *userResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A user that can authenticate to the cluster.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description:   "User UID.",
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"username": schema.StringAttribute{
				Description:   "User name. Changing it replaces the user.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"password": schema.StringAttribute{
				Description: "Password for the user. The API never returns it, so the configured value stays in state and a read that omits it is not a change. Changing it sets the password; other updates do not.",
				Required:    true,
				Sensitive:   true,
			},
			"role": schema.StringAttribute{
				Description:   "Role of the user (ClusterAdmin, OrgAdmin, ReadOnly, Regular, S3, or CSI). Server default on create; removing this later keeps the current value rather than reverting, since the API has no way to ask for the default back.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators: []validator.String{
					stringvalidator.OneOf(
						string(wekaapi.ClusterAdmin),
						string(wekaapi.OrgAdmin),
						string(wekaapi.ReadOnly),
						string(wekaapi.Regular),
						string(wekaapi.S3),
						string(wekaapi.CSI),
					),
				},
			},
			"posix_uid": schema.Int64Attribute{
				Description:   "POSIX UID for this user's S3 files. Server default on create; removing this later keeps the current value rather than reverting, since the API has no way to ask for the default back. Stored as a float32, exact only up to 2^24.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
				Validators:    []validator.Int64{int64validator.Between(0, maxExactSeconds)},
			},
			"posix_gid": schema.Int64Attribute{
				Description:   "POSIX GID for this user's S3 files. Server default on create; removing this later keeps the current value rather than reverting, since the API has no way to ask for the default back. Stored as a float32, exact only up to 2^24.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
				Validators:    []validator.Int64{int64validator.Between(0, maxExactSeconds)},
			},
		},
	}
}

func (r *userResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.api = configuredAPI(req.ProviderData, &resp.Diagnostics)
}

func (r *userResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan userModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	out, err := r.api.CreateUserWithResponse(ctx, wekaapi.CreateUserJSONRequestBody{
		Username: plan.Username.ValueStringPointer(),
		Password: plan.Password.ValueStringPointer(),
		Role:     rolePtr(plan.Role),
		PosixUid: secondsPtr(plan.PosixUID),
		PosixGid: secondsPtr(plan.PosixGID),
	})
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err == nil && (out.JSON200 == nil || out.JSON200.Data == nil || out.JSON200.Data.Uid == nil) {
		err = errors.New("create response has no uid")
	}
	if err != nil {
		resp.Diagnostics.AddError("Creating user", err.Error())
		return
	}
	m := userFromAPI(*out.JSON200.Data)
	m.Password = plan.Password
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *userResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state userModel
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
		resp.Diagnostics.AddError("Reading user", err.Error())
		return
	}
	// GetUsers has no password field. Keep the configured value so a refresh
	// is not a diff.
	m.Password = state.Password
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *userResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state, cfg userModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	uid := state.ID.ValueString()
	// Posix ids are sent only when configured. The server default read back
	// into state does not survive float32, and sending it fails the update.
	body := wekaapi.UpdateUserJSONRequestBody{}
	if !plan.Role.Equal(state.Role) {
		body.Role = rolePtr(plan.Role)
	}
	if !cfg.PosixUID.IsNull() && !cfg.PosixUID.IsUnknown() && !plan.PosixUID.Equal(state.PosixUID) {
		body.PosixUid = secondsPtr(cfg.PosixUID)
	}
	if !cfg.PosixGID.IsNull() && !cfg.PosixGID.IsUnknown() && !plan.PosixGID.Equal(state.PosixGID) {
		body.PosixGid = secondsPtr(cfg.PosixGID)
	}
	if body.Role != nil || body.PosixUid != nil || body.PosixGid != nil {
		// The raw call keeps a 400 body when data is a string. WithResponse
		// drops that body because it expects N400.data to be an object.
		rsp, err := r.api.UpdateUser(ctx, uid, body)
		if err == nil {
			var raw []byte
			raw, err = io.ReadAll(rsp.Body)
			_ = rsp.Body.Close()
			if err == nil {
				err = wekaapi.Check(rsp.StatusCode, raw)
			}
		}
		if err != nil {
			resp.Diagnostics.AddError("Updating user", err.Error())
			return
		}
	}
	// updateUser has no password. setUserPassword is the admin set, not the
	// old-password flow.
	if !plan.Password.Equal(state.Password) {
		out, err := r.api.SetUserPasswordWithResponse(ctx, uid, wekaapi.SetUserPasswordJSONRequestBody{
			Password: plan.Password.ValueString(),
		})
		if err == nil {
			err = wekaapi.Check(out.StatusCode(), out.Body)
		}
		if err != nil {
			resp.Diagnostics.AddError("Setting user password", err.Error())
			return
		}
	}

	m, err := r.get(ctx, uid)
	if err != nil {
		resp.Diagnostics.AddError("Updating user", err.Error())
		return
	}
	m.Password = plan.Password
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *userResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state userModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	out, err := r.api.DeleteUserWithResponse(ctx, state.ID.ValueString())
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil && !wekaapi.IsNotFound(err) {
		resp.Diagnostics.AddError("Deleting user", err.Error())
	}
}

// ImportState accepts a UID or a username, so existing users can be adopted by
// the name people already know. The API does not return a password; the next
// plan sets it from configuration.
func (r *userResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	users, err := r.list(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Importing user", err.Error())
		return
	}
	for _, u := range users {
		if deref(u.Uid) == req.ID || deref(u.Username) == req.ID {
			resp.Diagnostics.Append(resp.State.Set(ctx, userFromAPI(u))...)
			return
		}
	}
	resp.Diagnostics.AddError("Importing user", fmt.Sprintf("no user with UID or username %q", req.ID))
}

// get lists users. There is no get-by-uid; a missing UID is a 404 so Read
// drops the resource.
func (r *userResource) get(ctx context.Context, uid string) (userModel, error) {
	users, err := r.list(ctx)
	if err != nil {
		return userModel{}, err
	}
	for _, u := range users {
		if deref(u.Uid) == uid {
			return userFromAPI(u), nil
		}
	}
	return userModel{}, &wekaapi.APIError{Status: http.StatusNotFound, Message: "user not found"}
}

func (r *userResource) list(ctx context.Context) ([]wekaapi.User, error) {
	out, err := r.api.GetUsersWithResponse(ctx)
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil {
		return nil, err
	}
	if out.JSON200 == nil {
		return nil, errors.New("get response has no data")
	}
	if out.JSON200.Data == nil {
		return nil, nil
	}
	return *out.JSON200.Data, nil
}

func userFromAPI(u wekaapi.User) userModel {
	role := types.StringNull()
	if u.Role != nil {
		role = types.StringValue(*u.Role)
	}
	return userModel{
		ID:       types.StringValue(deref(u.Uid)),
		Username: types.StringValue(deref(u.Username)),
		Role:     role,
		PosixUID: secondsValue(u.PosixUid),
		PosixGID: secondsValue(u.PosixGid),
	}
}

func rolePtr(v types.String) *wekaapi.UserRole {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	role := wekaapi.UserRole(v.ValueString())
	return &role
}
