package provider

import (
	"fmt"
	"net/http"
	"regexp"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"gitlab.com/absci/devops/terraform-weka/internal/wekaapi"
)

// fakeFileSystemGroups adds /fileSystemGroups to f and returns its store.
func fakeFileSystemGroups(t *testing.T, f *fakeWeka) *sync.Map {
	var store sync.Map
	var mu sync.Mutex
	next := 0
	f32 := func(v float32) *float32 { return &v }

	f.Handle("POST /fileSystemGroups", func(w http.ResponseWriter, r *http.Request) {
		var body wekaapi.CreateFileSystemGroupJSONBody
		decodeBody(t, r, &body)
		mu.Lock()
		next++
		uid := fmt.Sprintf("uid-%d", next)
		mu.Unlock()
		g := wekaapi.FileSystemGroup{Uid: &uid, Name: &body.Name, TargetSsdRetention: f32(86400), StartDemote: f32(1800)}
		if body.TargetSsdRetention != nil {
			g.TargetSsdRetention = body.TargetSsdRetention
		}
		if body.StartDemote != nil {
			g.StartDemote = body.StartDemote
		}
		store.Store(uid, g)
		writeData(w, g)
	})
	f.Handle("GET /fileSystemGroups", func(w http.ResponseWriter, r *http.Request) {
		var all []wekaapi.FileSystemGroup
		store.Range(func(_, v any) bool { all = append(all, v.(wekaapi.FileSystemGroup)); return true })
		writeData(w, all)
	})
	f.Handle("GET /fileSystemGroups/{uid}", func(w http.ResponseWriter, r *http.Request) {
		g, ok := store.Load(r.PathValue("uid"))
		if !ok {
			writeError(w, http.StatusNotFound, "filesystem group not found")
			return
		}
		writeData(w, g)
	})
	f.Handle("PUT /fileSystemGroups/{uid}", func(w http.ResponseWriter, r *http.Request) {
		v, ok := store.Load(r.PathValue("uid"))
		if !ok {
			writeError(w, http.StatusNotFound, "filesystem group not found")
			return
		}
		g := v.(wekaapi.FileSystemGroup)
		var body wekaapi.UpdateFileSystemGroupJSONBody
		decodeBody(t, r, &body)
		if body.NewName != nil {
			g.Name = body.NewName
		}
		if body.TargetSsdRetention != nil {
			g.TargetSsdRetention = body.TargetSsdRetention
		}
		if body.StartDemote != nil {
			g.StartDemote = body.StartDemote
		}
		store.Store(*g.Uid, g)
		writeData(w, g)
	})
	f.Handle("DELETE /fileSystemGroups/{uid}", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := store.LoadAndDelete(r.PathValue("uid")); !ok {
			writeError(w, http.StatusNotFound, "filesystem group not found")
			return
		}
		writeData(w, "ok")
	})
	return &store
}

func TestFileSystemGroupResource(t *testing.T) {
	f := newFakeWeka(t)
	store := fakeFileSystemGroups(t, f)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			left := 0
			store.Range(func(_, _ any) bool { left++; return true })
			if left != 0 {
				return fmt.Errorf("%d filesystem groups left after destroy", left)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: f.config(`
resource "weka_filesystem_group" "test" {
  name = "tier"
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("weka_filesystem_group.test", "id", "uid-1"),
					resource.TestCheckResourceAttr("weka_filesystem_group.test", "name", "tier"),
					resource.TestCheckResourceAttr("weka_filesystem_group.test", "target_ssd_retention", "86400"),
					resource.TestCheckResourceAttr("weka_filesystem_group.test", "start_demote", "1800"),
				),
			},
			{
				Config: f.config(`
resource "weka_filesystem_group" "test" {
  name                 = "tier2"
  target_ssd_retention = 3600
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("weka_filesystem_group.test", "id", "uid-1"),
					resource.TestCheckResourceAttr("weka_filesystem_group.test", "name", "tier2"),
					resource.TestCheckResourceAttr("weka_filesystem_group.test", "target_ssd_retention", "3600"),
					resource.TestCheckResourceAttr("weka_filesystem_group.test", "start_demote", "1800"),
				),
			},
			{
				ResourceName:      "weka_filesystem_group.test",
				ImportState:       true,
				ImportStateId:     "tier2",
				ImportStateVerify: true,
			},
			{
				// 2^24 + 1: the first value a float32 cannot represent exactly.
				Config: f.config(`
resource "weka_filesystem_group" "test" {
  name                  = "tier2"
  target_ssd_retention  = 16777217
}`),
				ExpectError: regexp.MustCompile(`(?s)Attribute target_ssd_retention.*between 0 and 16777216`),
			},
			{
				// Deleted outside Terraform: Read drops it and the plan recreates it.
				PreConfig: func() { store.Delete("uid-1") },
				Config: f.config(`
resource "weka_filesystem_group" "test" {
  name                 = "tier2"
  target_ssd_retention = 3600
}`),
				Check: resource.TestCheckResourceAttr("weka_filesystem_group.test", "id", "uid-2"),
			},
		},
	})
}
