package provider

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"gitlab.com/absci/devops/terraform-provider-weka/internal/wekaapi"
)

// accPrefix is the only name prefix acceptance tests may create or delete.
// Anything else on the cluster is left alone.
const accPrefix = "tfacc-"

func TestAccFileSystemGroup(t *testing.T) {
	// resource.Test skips unless TF_ACC=1, so CI never touches a cluster.
	api := accAPI(t)
	name := accName()
	renamed := name + "b"

	var baseline map[string]string
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		PreCheck: func() {
			accPreCheck(t)
			baseline = groupNames(t, api)
			for n, uid := range baseline {
				if strings.HasPrefix(n, accPrefix) {
					// A previous run died before cleanup. Only this prefix is ours.
					accDelete(t, api, n, uid)
					delete(baseline, n)
				}
			}
		},
		CheckDestroy: func(s *terraform.State) error {
			after := groupNames(t, api)
			for n := range baseline {
				if _, ok := after[n]; !ok {
					return fmt.Errorf("pre-existing filesystem group %q is gone", n)
				}
			}
			for n := range after {
				if strings.HasPrefix(n, accPrefix) {
					return fmt.Errorf("temporary filesystem group %q was not removed", n)
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: accConfig(name, "target_ssd_retention = 180\n  start_demote = 60"),
				Check: resource.ComposeAggregateTestCheckFunc(
					accMustBeOurs(name),
					resource.TestCheckResourceAttr("weka_filesystem_group.test", "name", name),
					resource.TestCheckResourceAttr("weka_filesystem_group.test", "target_ssd_retention", "180"),
					resource.TestCheckResourceAttr("weka_filesystem_group.test", "start_demote", "60"),
					resource.TestCheckResourceAttrSet("weka_filesystem_group.test", "id"),
				),
			},
			{
				Config: accConfig(renamed, "target_ssd_retention = 180\n  start_demote = 60"),
				Check: resource.ComposeAggregateTestCheckFunc(
					accMustBeOurs(renamed),
					resource.TestCheckResourceAttr("weka_filesystem_group.test", "name", renamed),
					resource.TestCheckResourceAttr("weka_filesystem_group.test", "target_ssd_retention", "180"),
				),
			},
			{
				// 1080 and 360 days, the values a live cluster uses. Past 2^24, still exact as float32.
				Config: accConfig(renamed, "target_ssd_retention = 93312000\n  start_demote = 31104000"),
				Check: resource.ComposeAggregateTestCheckFunc(
					accMustBeOurs(renamed),
					resource.TestCheckResourceAttr("weka_filesystem_group.test", "target_ssd_retention", "93312000"),
					resource.TestCheckResourceAttr("weka_filesystem_group.test", "start_demote", "31104000"),
				),
			},
			{
				ResourceName:      "weka_filesystem_group.test",
				ImportState:       true,
				ImportStateId:     renamed,
				ImportStateVerify: true,
			},
		},
	})
}

func accPreCheck(t *testing.T) {
	t.Helper()
	for _, k := range []string{"WEKA_ENDPOINT", "WEKA_USERNAME", "WEKA_PASSWORD"} {
		if os.Getenv(k) == "" {
			t.Fatalf("%s must be set", k)
		}
	}
}

func accAPI(t *testing.T) *wekaapi.API {
	t.Helper()
	if os.Getenv("TF_ACC") == "" {
		return nil
	}
	insecure, _ := strconv.ParseBool(os.Getenv("WEKA_INSECURE"))
	api, err := wekaapi.New(wekaapi.Config{
		Endpoint:           os.Getenv("WEKA_ENDPOINT"),
		Org:                os.Getenv("WEKA_ORG"),
		Username:           os.Getenv("WEKA_USERNAME"),
		Password:           os.Getenv("WEKA_PASSWORD"),
		InsecureSkipVerify: insecure,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { accSweep(t, api) })
	return api
}

func accConfig(name, attrs string) string {
	if !strings.HasPrefix(name, accPrefix) {
		panic("acceptance config name " + name + " is outside " + accPrefix)
	}
	return fmt.Sprintf(`
provider "weka" {}

resource "weka_filesystem_group" "test" {
  name = %q
  %s
}
`, name, attrs)
}

func accMustBeOurs(name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources["weka_filesystem_group.test"]
		if !ok {
			return fmt.Errorf("weka_filesystem_group.test missing from state")
		}
		if rs.Primary.Attributes["name"] != name || !strings.HasPrefix(name, accPrefix) {
			return fmt.Errorf("refusing to keep going: resource name %q is not %q", rs.Primary.Attributes["name"], name)
		}
		return nil
	}
}

func accName() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return accPrefix + hex.EncodeToString(b[:])
}

func groupNames(t *testing.T, api *wekaapi.API) map[string]string {
	t.Helper()
	out, err := api.GetFileSystemGroupsWithResponse(context.Background())
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
	for _, g := range *out.JSON200.Data {
		names[deref(g.Name)] = deref(g.Uid)
	}
	return names
}

// accDelete removes one group. It refuses any name outside accPrefix.
func accDelete(t *testing.T, api *wekaapi.API, name, uid string) {
	t.Helper()
	if !strings.HasPrefix(name, accPrefix) || uid == "" {
		t.Fatalf("refusing to delete filesystem group %q (%s)", name, uid)
	}
	out, err := api.DeleteFileSystemGroupWithResponse(context.Background(), uid)
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil && !wekaapi.IsNotFound(err) {
		t.Fatal(err)
	}
}

// accSweep removes whatever this run left behind. It never touches other names.
func accSweep(t *testing.T, api *wekaapi.API) {
	t.Helper()
	for name, uid := range groupNames(t, api) {
		if strings.HasPrefix(name, accPrefix) {
			accDelete(t, api, name, uid)
		}
	}
}
