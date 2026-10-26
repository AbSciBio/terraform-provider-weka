package provider

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"gitlab.com/absci/devops/terraform-provider-weka/internal/wekaapi"
)

func TestAccFileSystem(t *testing.T) {
	// resource.Test skips unless TF_ACC=1, so CI never touches a cluster.
	api := accAPI(t)
	if api != nil {
		// Filesystems first: a group delete fails while one of its filesystems
		// still exists. accAPI already sweeps tfacc- groups afterwards.
		t.Cleanup(func() { accSweepFileSystems(t, api) })
	}
	group := accName()
	name := accName()
	renamed := name + "b"

	var baseGroups, baseFS map[string]string
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		PreCheck: func() {
			accPreCheck(t)
			baseFS = fileSystemNames(t, api)
			baseGroups = groupNames(t, api)
			for n, uid := range baseFS {
				if strings.HasPrefix(n, accPrefix) {
					accDeleteFileSystem(t, api, n, uid)
					delete(baseFS, n)
				}
			}
			for n, uid := range baseGroups {
				if strings.HasPrefix(n, accPrefix) {
					accDelete(t, api, n, uid)
					delete(baseGroups, n)
				}
			}
		},
		CheckDestroy: func(*terraform.State) error {
			afterGroups := groupNames(t, api)
			for n := range baseGroups {
				if _, ok := afterGroups[n]; !ok {
					return fmt.Errorf("pre-existing filesystem group %q is gone", n)
				}
			}
			afterFS := fileSystemNames(t, api)
			for n := range baseFS {
				if _, ok := afterFS[n]; !ok {
					return fmt.Errorf("pre-existing filesystem %q is gone", n)
				}
			}
			for n := range afterGroups {
				if strings.HasPrefix(n, accPrefix) {
					return fmt.Errorf("temporary filesystem group %q was not removed", n)
				}
			}
			for n := range afterFS {
				if strings.HasPrefix(n, accPrefix) {
					return fmt.Errorf("temporary filesystem %q was not removed", n)
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: accFileSystemConfig(group, name, 1073741824, "ssd_capacity   = 1073741824\n  auth_required  = false"),
				Check: resource.ComposeAggregateTestCheckFunc(
					accFileSystemMustBeOurs(group, name),
					resource.TestCheckResourceAttr("weka_filesystem.test", "name", name),
					resource.TestCheckResourceAttr("weka_filesystem.test", "group_name", group),
					resource.TestCheckResourceAttr("weka_filesystem.test", "total_capacity", "1073741824"),
					resource.TestCheckResourceAttr("weka_filesystem.test", "ssd_capacity", "1073741824"),
					resource.TestCheckResourceAttr("weka_filesystem.test", "auth_required", "false"),
					resource.TestCheckResourceAttrSet("weka_filesystem.test", "id"),
				),
			},
			{
				Config: accFileSystemConfig(group, renamed, 2147483648, "ssd_capacity   = 1073741824\n  auth_required  = true"),
				Check: resource.ComposeAggregateTestCheckFunc(
					accFileSystemMustBeOurs(group, renamed),
					resource.TestCheckResourceAttr("weka_filesystem.test", "name", renamed),
					resource.TestCheckResourceAttr("weka_filesystem.test", "group_name", group),
					resource.TestCheckResourceAttr("weka_filesystem.test", "total_capacity", "2147483648"),
					resource.TestCheckResourceAttr("weka_filesystem.test", "auth_required", "true"),
				),
			},
			{
				ResourceName:      "weka_filesystem.test",
				ImportState:       true,
				ImportStateId:     renamed,
				ImportStateVerify: true,
			},
		},
	})
}

func accFileSystemConfig(group, name string, total int64, attrs string) string {
	if !strings.HasPrefix(group, accPrefix) || !strings.HasPrefix(name, accPrefix) {
		panic("acceptance config name is outside " + accPrefix)
	}
	return fmt.Sprintf(`
provider "weka" {}

resource "weka_filesystem_group" "test" {
  name = %q
}

resource "weka_filesystem" "test" {
  name           = %q
  group_name     = weka_filesystem_group.test.name
  total_capacity = %d
  %s
}
`, group, name, total, attrs)
}

func accFileSystemMustBeOurs(group, name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		gs, ok := s.RootModule().Resources["weka_filesystem_group.test"]
		if !ok {
			return fmt.Errorf("weka_filesystem_group.test missing from state")
		}
		fs, ok := s.RootModule().Resources["weka_filesystem.test"]
		if !ok {
			return fmt.Errorf("weka_filesystem.test missing from state")
		}
		if gs.Primary.Attributes["name"] != group || !strings.HasPrefix(group, accPrefix) {
			return fmt.Errorf("refusing to keep going: filesystem group name %q is not %q", gs.Primary.Attributes["name"], group)
		}
		if fs.Primary.Attributes["name"] != name || !strings.HasPrefix(name, accPrefix) {
			return fmt.Errorf("refusing to keep going: filesystem name %q is not %q", fs.Primary.Attributes["name"], name)
		}
		return nil
	}
}

func fileSystemNames(t *testing.T, api *wekaapi.API) map[string]string {
	t.Helper()
	out, err := api.GetFileSystemsWithResponse(context.Background(), nil)
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	if out.JSON200 == nil || out.JSON200.Data == nil {
		return names
	}
	for _, fs := range *out.JSON200.Data {
		names[deref(fs.Name)] = deref(fs.Uid)
	}
	return names
}

// accDeleteFileSystem removes one filesystem. It refuses any name outside accPrefix.
func accDeleteFileSystem(t *testing.T, api *wekaapi.API, name, uid string) {
	t.Helper()
	if !strings.HasPrefix(name, accPrefix) || uid == "" {
		t.Fatalf("refusing to delete filesystem %q (%s)", name, uid)
	}
	out, err := api.DeleteFileSystemWithResponse(context.Background(), uid, nil)
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil && !wekaapi.IsNotFound(err) {
		t.Fatal(err)
	}
}

// accSweepFileSystems removes whatever this run left behind. It never touches other names.
func accSweepFileSystems(t *testing.T, api *wekaapi.API) {
	t.Helper()
	for name, uid := range fileSystemNames(t, api) {
		if strings.HasPrefix(name, accPrefix) {
			accDeleteFileSystem(t, api, name, uid)
		}
	}
}
