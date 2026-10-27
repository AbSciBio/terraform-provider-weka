package provider

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/AbSciBio/terraform-provider-weka/internal/wekaapi"
)

func TestAccObjectStoreBucket(t *testing.T) {
	// Weka contacts the bucket during create and rejects a placeholder endpoint.
	if os.Getenv("WEKA_OBS_HOSTNAME") == "" || os.Getenv("WEKA_OBS_BUCKET") == "" || os.Getenv("WEKA_OBS_REGION") == "" || os.Getenv("WEKA_OBS_ACCESS_KEY_ID") == "" || os.Getenv("WEKA_OBS_SECRET_KEY") == "" {
		t.Skip("set WEKA_OBS_HOSTNAME, WEKA_OBS_BUCKET, WEKA_OBS_REGION, WEKA_OBS_ACCESS_KEY_ID, and WEKA_OBS_SECRET_KEY to a bucket this cluster can verify")
	}
	api := accAPI(t)
	if api != nil {
		t.Cleanup(func() { sweepObjectStoreBuckets(t, api) })
	}
	name := accName()
	renamed := name + "b"

	var baseline map[string]string
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		PreCheck: func() {
			accPreCheck(t)
			baseline = objectStoreBucketNames(t, api)
			for n, uid := range baseline {
				if strings.HasPrefix(n, accPrefix) {
					// A previous run died before cleanup. Only this prefix is ours.
					deleteObjectStoreBucket(t, api, n, uid)
					delete(baseline, n)
				}
			}
		},
		CheckDestroy: func(s *terraform.State) error {
			after := objectStoreBucketNames(t, api)
			for n := range baseline {
				if _, ok := after[n]; !ok {
					return fmt.Errorf("pre-existing object store bucket %q is gone", n)
				}
			}
			for n := range after {
				if strings.HasPrefix(n, accPrefix) {
					return fmt.Errorf("temporary object store bucket %q was not removed", n)
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: objectStoreBucketAccConfig(name),
				Check: resource.ComposeAggregateTestCheckFunc(
					accBucketMustBeOurs(name),
					resource.TestCheckResourceAttr("weka_object_store_bucket.test", "name", name),
					resource.TestCheckResourceAttr("weka_object_store_bucket.test", "hostname", os.Getenv("WEKA_OBS_HOSTNAME")),
					resource.TestCheckResourceAttrSet("weka_object_store_bucket.test", "id"),
				),
			},
			{
				Config: objectStoreBucketAccConfig(renamed),
				Check: resource.ComposeAggregateTestCheckFunc(
					accBucketMustBeOurs(renamed),
					resource.TestCheckResourceAttr("weka_object_store_bucket.test", "name", renamed),
				),
			},
			{
				ResourceName:            "weka_object_store_bucket.test",
				ImportState:             true,
				ImportStateId:           renamed,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"secret_key"},
			},
		},
	})
}

func objectStoreBucketAccConfig(name string) string {
	if !strings.HasPrefix(name, accPrefix) {
		panic("acceptance config name " + name + " is outside " + accPrefix)
	}
	return fmt.Sprintf(`
provider "weka" {}

resource "weka_object_store_bucket" "test" {
  name          = %q
  obs_name      = "default-local"
  protocol      = "HTTPS"
  hostname      = %q
  port          = 443
  bucket        = %q
  auth_method   = "AWSSignature4"
  region        = %q
  access_key_id = %q
  secret_key    = %q
}
`, name, os.Getenv("WEKA_OBS_HOSTNAME"), os.Getenv("WEKA_OBS_BUCKET"), os.Getenv("WEKA_OBS_REGION"), os.Getenv("WEKA_OBS_ACCESS_KEY_ID"), os.Getenv("WEKA_OBS_SECRET_KEY"))
}

func accBucketMustBeOurs(name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources["weka_object_store_bucket.test"]
		if !ok {
			return fmt.Errorf("weka_object_store_bucket.test missing from state")
		}
		if rs.Primary.Attributes["name"] != name || !strings.HasPrefix(name, accPrefix) {
			return fmt.Errorf("refusing to keep going: resource name %q is not %q", rs.Primary.Attributes["name"], name)
		}
		return nil
	}
}

func objectStoreBucketNames(t *testing.T, api *wekaapi.API) map[string]string {
	t.Helper()
	out, err := api.GetAllObsBucketsWithResponse(context.Background())
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
	for _, b := range *out.JSON200.Data {
		names[deref(b.Name)] = deref(b.Uid)
	}
	return names
}

// deleteObjectStoreBucket removes one bucket. It refuses any name outside accPrefix.
func deleteObjectStoreBucket(t *testing.T, api *wekaapi.API, name, uid string) {
	t.Helper()
	if !strings.HasPrefix(name, accPrefix) || uid == "" {
		t.Fatalf("refusing to delete object store bucket %q (%s)", name, uid)
	}
	out, err := api.DeleteObsBucketWithResponse(context.Background(), uid)
	if err == nil {
		err = wekaapi.Check(out.StatusCode(), out.Body)
	}
	if err != nil && !wekaapi.IsNotFound(err) {
		t.Fatal(err)
	}
}

// sweepObjectStoreBuckets removes whatever this run left behind. It never touches other names.
func sweepObjectStoreBuckets(t *testing.T, api *wekaapi.API) {
	t.Helper()
	for name, uid := range objectStoreBucketNames(t, api) {
		if strings.HasPrefix(name, accPrefix) {
			deleteObjectStoreBucket(t, api, name, uid)
		}
	}
}
