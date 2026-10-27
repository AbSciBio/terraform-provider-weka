package provider

import (
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"gitlab.com/absci/devops/terraform-provider-weka/internal/wekaapi"
)

// fakeFileSystems adds /fileSystems to f and returns its store.
// Create stores a not-ready filesystem and returns that. The first read
// marks it ready and fills server defaults, so a resource that skips the
// ready poll keeps null ssd_capacity, auth_required, and encrypted.
func fakeFileSystems(t *testing.T, f *fakeWeka) *sync.Map {
	var store sync.Map
	var mu sync.Mutex
	next := 0
	boolp := func(v bool) *bool { return &v }
	i64p := func(v int64) *int64 { return &v }

	f.Handle("POST /fileSystems", func(w http.ResponseWriter, r *http.Request) {
		var body wekaapi.CreateFileSystemJSONBody
		decodeBody(t, r, &body)
		mu.Lock()
		next++
		uid := fmt.Sprintf("fs-%d", next)
		mu.Unlock()
		creating, ready := true, false
		fs := wekaapi.FileSystem{
			Uid:          &uid,
			Name:         &body.Name,
			GroupName:    &body.GroupName,
			TotalBudget:  &body.TotalCapacity,
			SsdBudget:    body.SsdCapacity,
			AuthRequired: body.AuthRequired,
			IsEncrypted:  body.Encrypted,
			IsCreating:   &creating,
			IsReady:      &ready,
		}
		store.Store(uid, fs)
		writeData(w, fs)
	})
	f.Handle("GET /fileSystems", func(w http.ResponseWriter, r *http.Request) {
		var all []wekaapi.FileSystem
		store.Range(func(_, v any) bool {
			all = append(all, v.(wekaapi.FileSystem))
			return true
		})
		writeData(w, all)
	})
	f.Handle("GET /fileSystems/{uid}", func(w http.ResponseWriter, r *http.Request) {
		v, ok := store.Load(r.PathValue("uid"))
		if !ok {
			writeError(w, http.StatusNotFound, "filesystem not found")
			return
		}
		fs := v.(wekaapi.FileSystem)
		if fileSystemPending(fs) {
			if fs.SsdBudget == nil && fs.TotalBudget != nil {
				fs.SsdBudget = i64p(*fs.TotalBudget)
			}
			if fs.AuthRequired == nil {
				fs.AuthRequired = boolp(false)
			}
			if fs.IsEncrypted == nil {
				fs.IsEncrypted = boolp(false)
			}
			fs.IsCreating = boolp(false)
			fs.IsReady = boolp(true)
			store.Store(r.PathValue("uid"), fs)
		}
		writeData(w, fs)
	})
	f.Handle("PUT /fileSystems/{uid}", func(w http.ResponseWriter, r *http.Request) {
		v, ok := store.Load(r.PathValue("uid"))
		if !ok {
			writeError(w, http.StatusNotFound, "filesystem not found")
			return
		}
		fs := v.(wekaapi.FileSystem)
		var body wekaapi.UpdateFileSystemJSONBody
		decodeBody(t, r, &body)
		if body.NewName != nil {
			fs.Name = body.NewName
		}
		if body.TotalCapacity != nil {
			fs.TotalBudget = body.TotalCapacity
		}
		if body.SsdCapacity != nil {
			fs.SsdBudget = body.SsdCapacity
		} else if fs.TotalBudget != nil {
			// A non-tiered filesystem stores SSD equal to total when SSD is omitted.
			fs.SsdBudget = i64p(*fs.TotalBudget)
		}
		if body.AuthRequired != nil {
			fs.AuthRequired = body.AuthRequired
		}
		store.Store(*fs.Uid, fs)
		writeData(w, fs)
	})
	f.Handle("DELETE /fileSystems/{uid}", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := store.LoadAndDelete(r.PathValue("uid")); !ok {
			writeError(w, http.StatusNotFound, "filesystem not found")
			return
		}
		writeData(w, "ok")
	})
	return &store
}

func TestFileSystemResource(t *testing.T) {
	f := newFakeWeka(t)
	store := fakeFileSystems(t, f)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			left := 0
			store.Range(func(_, _ any) bool { left++; return true })
			if left != 0 {
				return fmt.Errorf("%d filesystems left after destroy", left)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: f.config(`
resource "weka_filesystem" "test" {
  name           = "data"
  group_name     = "tier"
  total_capacity = 1073741824
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("weka_filesystem.test", "id", "fs-1"),
					resource.TestCheckResourceAttr("weka_filesystem.test", "name", "data"),
					resource.TestCheckResourceAttr("weka_filesystem.test", "group_name", "tier"),
					resource.TestCheckResourceAttr("weka_filesystem.test", "total_capacity", "1073741824"),
					resource.TestCheckResourceAttr("weka_filesystem.test", "ssd_capacity", "1073741824"),
					resource.TestCheckResourceAttr("weka_filesystem.test", "auth_required", "false"),
					resource.TestCheckResourceAttr("weka_filesystem.test", "encrypted", "false"),
				),
			},
			{
				Config: f.config(`
resource "weka_filesystem" "test" {
  name           = "data"
  group_name     = "tier"
  total_capacity = 2147483648
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("weka_filesystem.test", "total_capacity", "2147483648"),
					resource.TestCheckResourceAttr("weka_filesystem.test", "ssd_capacity", "2147483648"),
				),
			},
			{
				Config: f.config(`
resource "weka_filesystem" "test" {
  name           = "data2"
  group_name     = "tier"
  total_capacity = 2147483648
  ssd_capacity   = 536870912
  auth_required  = true
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("weka_filesystem.test", "id", "fs-1"),
					resource.TestCheckResourceAttr("weka_filesystem.test", "name", "data2"),
					resource.TestCheckResourceAttr("weka_filesystem.test", "group_name", "tier"),
					resource.TestCheckResourceAttr("weka_filesystem.test", "total_capacity", "2147483648"),
					resource.TestCheckResourceAttr("weka_filesystem.test", "ssd_capacity", "536870912"),
					resource.TestCheckResourceAttr("weka_filesystem.test", "auth_required", "true"),
					resource.TestCheckResourceAttr("weka_filesystem.test", "encrypted", "false"),
				),
			},
			{
				ResourceName:      "weka_filesystem.test",
				ImportState:       true,
				ImportStateId:     "data2",
				ImportStateVerify: true,
			},
			{
				ResourceName:      "weka_filesystem.test",
				ImportState:       true,
				ImportStateId:     "fs-1",
				ImportStateVerify: true,
			},
			{
				// Deleted outside Terraform: Read drops it and the plan recreates it.
				PreConfig: func() { store.Delete("fs-1") },
				Config: f.config(`
resource "weka_filesystem" "test" {
  name           = "data2"
  group_name     = "tier"
  total_capacity = 2147483648
  ssd_capacity   = 536870912
  auth_required  = true
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("weka_filesystem.test", "id", "fs-2"),
					resource.TestCheckResourceAttr("weka_filesystem.test", "name", "data2"),
					resource.TestCheckResourceAttr("weka_filesystem.test", "total_capacity", "2147483648"),
				),
			},
		},
	})
}
