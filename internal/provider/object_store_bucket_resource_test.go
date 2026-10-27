package provider

import (
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/AbSciBio/terraform-provider-weka/internal/wekaapi"
)

// fakeObsBuckets adds /objectStoreBuckets to f and returns its store.
// Responses leave secret_key unset, which is what a GET that omits it looks like.
func fakeObsBuckets(t *testing.T, f *fakeWeka) *sync.Map {
	t.Helper()
	var store sync.Map
	var mu sync.Mutex
	next := 0
	str := func(s string) *string { return &s }
	f32 := func(v float32) *float32 { return &v }

	put := func(bodyName string, bodyObsName *string, site *string, obsType *string, protocol *string, hostname *string, port *float32, bucket *string, auth *string, region *string, accessKey *string) wekaapi.ObjectStoreBucket {
		mu.Lock()
		next++
		uid := fmt.Sprintf("uid-%d", next)
		mu.Unlock()
		b := wekaapi.ObjectStoreBucket{
			Uid:         &uid,
			Name:        str(bodyName),
			ObsName:     bodyObsName,
			ObsSite:     site,
			ObsType:     obsType,
			Protocol:    protocol,
			Hostname:    hostname,
			Port:        port,
			Bucket:      bucket,
			AuthMethod:  auth,
			Region:      region,
			AccessKeyId: accessKey,
		}
		if b.ObsType == nil {
			b.ObsType = str("AWS")
		}
		if b.Protocol == nil {
			b.Protocol = str("HTTP")
		}
		store.Store(uid, b)
		return b
	}

	f.Handle("POST /objectStoreBuckets", func(w http.ResponseWriter, r *http.Request) {
		var body wekaapi.CreateObsBucketJSONBody
		decodeBody(t, r, &body)
		var obsType, protocol, auth *string
		if body.ObsType != nil {
			obsType = str(string(*body.ObsType))
		}
		if body.Protocol != nil {
			protocol = str(string(*body.Protocol))
		}
		if body.AuthMethod != nil {
			auth = str(string(*body.AuthMethod))
		}
		var obsName *string
		if body.ObsName != nil {
			obsName = str(*body.ObsName)
		}
		var site, hostname, bucket, region, accessKey *string
		if body.ObsSite != nil {
			site = str(*body.ObsSite)
		}
		if body.Hostname != nil {
			hostname = str(*body.Hostname)
		}
		if body.Bucket != nil {
			bucket = str(*body.Bucket)
		}
		if body.Region != nil {
			region = str(*body.Region)
		}
		if body.AccessKeyId != nil {
			accessKey = str(*body.AccessKeyId)
		}
		var port *float32
		if body.Port != nil {
			port = f32(*body.Port)
		}
		writeData(w, put(body.Name, obsName, site, obsType, protocol, hostname, port, bucket, auth, region, accessKey))
	})
	f.Handle("GET /objectStoreBuckets", func(w http.ResponseWriter, r *http.Request) {
		var all []wekaapi.ObjectStoreBucket
		store.Range(func(_, v any) bool {
			all = append(all, v.(wekaapi.ObjectStoreBucket))
			return true
		})
		writeData(w, all)
	})
	f.Handle("GET /objectStoreBuckets/{uid}", func(w http.ResponseWriter, r *http.Request) {
		b, ok := store.Load(r.PathValue("uid"))
		if !ok {
			writeError(w, http.StatusNotFound, "object store bucket not found")
			return
		}
		writeData(w, b)
	})
	f.Handle("PUT /objectStoreBuckets/{uid}", func(w http.ResponseWriter, r *http.Request) {
		v, ok := store.Load(r.PathValue("uid"))
		if !ok {
			writeError(w, http.StatusNotFound, "object store bucket not found")
			return
		}
		b := v.(wekaapi.ObjectStoreBucket)
		var body wekaapi.UpdateObsBucketJSONBody
		decodeBody(t, r, &body)
		if body.NewName != nil {
			b.Name = str(*body.NewName)
		}
		if body.NewObsName != nil {
			b.ObsName = str(*body.NewObsName)
		}
		if body.Hostname != nil {
			b.Hostname = str(*body.Hostname)
		}
		if body.Bucket != nil {
			b.Bucket = str(*body.Bucket)
		}
		if body.Port != nil {
			b.Port = f32(*body.Port)
		}
		if body.Protocol != nil {
			b.Protocol = str(string(*body.Protocol))
		}
		if body.AuthMethod != nil {
			b.AuthMethod = str(string(*body.AuthMethod))
		}
		if body.Region != nil {
			b.Region = str(*body.Region)
		}
		if body.AccessKeyId != nil {
			b.AccessKeyId = str(*body.AccessKeyId)
		}
		store.Store(*b.Uid, b)
		writeData(w, b)
	})
	f.Handle("DELETE /objectStoreBuckets/{uid}", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := store.LoadAndDelete(r.PathValue("uid")); !ok {
			writeError(w, http.StatusNotFound, "object store bucket not found")
			return
		}
		writeData(w, "ok")
	})
	return &store
}

func TestObjectStoreBucketResource(t *testing.T) {
	f := newFakeWeka(t)
	store := fakeObsBuckets(t, f)

	const created = `
resource "weka_object_store_bucket" "test" {
  name          = "cold"
  obs_name      = "default"
  obs_site      = "local"
  obs_type      = "AWS"
  protocol      = "HTTPS"
  hostname      = "s3.example.com"
  port          = 443
  bucket        = "cold-data"
  auth_method   = "AWSSignature4"
  region        = "us-east-1"
  access_key_id = "placeholder-access-key"
  secret_key    = "placeholder-secret-key"
}`
	const updated = `
resource "weka_object_store_bucket" "test" {
  name          = "cold"
  obs_name      = "default"
  obs_site      = "local"
  obs_type      = "AWS"
  protocol      = "HTTPS"
  hostname      = "s3.backup.example.com"
  port          = 443
  bucket        = "cold-data"
  auth_method   = "AWSSignature4"
  region        = "us-east-1"
  access_key_id = "placeholder-access-key"
  secret_key    = "placeholder-secret-key"
}`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			left := 0
			store.Range(func(_, _ any) bool { left++; return true })
			if left != 0 {
				return fmt.Errorf("%d object store buckets left after destroy", left)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: f.config(created),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("weka_object_store_bucket.test", "id", "uid-1"),
					resource.TestCheckResourceAttr("weka_object_store_bucket.test", "name", "cold"),
					resource.TestCheckResourceAttr("weka_object_store_bucket.test", "obs_name", "default"),
					resource.TestCheckResourceAttr("weka_object_store_bucket.test", "obs_site", "local"),
					resource.TestCheckResourceAttr("weka_object_store_bucket.test", "obs_type", "AWS"),
					resource.TestCheckResourceAttr("weka_object_store_bucket.test", "protocol", "HTTPS"),
					resource.TestCheckResourceAttr("weka_object_store_bucket.test", "hostname", "s3.example.com"),
					resource.TestCheckResourceAttr("weka_object_store_bucket.test", "port", "443"),
					resource.TestCheckResourceAttr("weka_object_store_bucket.test", "bucket", "cold-data"),
					resource.TestCheckResourceAttr("weka_object_store_bucket.test", "auth_method", "AWSSignature4"),
					resource.TestCheckResourceAttr("weka_object_store_bucket.test", "region", "us-east-1"),
					resource.TestCheckResourceAttr("weka_object_store_bucket.test", "access_key_id", "placeholder-access-key"),
					resource.TestCheckResourceAttr("weka_object_store_bucket.test", "secret_key", "placeholder-secret-key"),
				),
			},
			{
				Config: f.config(updated),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("weka_object_store_bucket.test", "id", "uid-1"),
					resource.TestCheckResourceAttr("weka_object_store_bucket.test", "hostname", "s3.backup.example.com"),
					resource.TestCheckResourceAttr("weka_object_store_bucket.test", "bucket", "cold-data"),
					resource.TestCheckResourceAttr("weka_object_store_bucket.test", "secret_key", "placeholder-secret-key"),
				),
			},
			{
				ResourceName:            "weka_object_store_bucket.test",
				ImportState:             true,
				ImportStateId:           "cold",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"secret_key"},
			},
			{
				ResourceName:            "weka_object_store_bucket.test",
				ImportState:             true,
				ImportStateId:           "uid-1",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"secret_key"},
			},
			{
				// Deleted outside Terraform: Read drops it and the plan recreates it.
				PreConfig: func() { store.Delete("uid-1") },
				Config:    f.config(updated),
				Check:     resource.TestCheckResourceAttr("weka_object_store_bucket.test", "id", "uid-2"),
			},
		},
	})
}
