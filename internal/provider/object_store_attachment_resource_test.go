package provider

import (
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

type fakeAttachedBucket struct {
	Name string `json:"name"`
	Uid  string `json:"uid"`
	Mode string `json:"mode"`
}

// fakeObjectStoreAttachments adds the object-store-bucket side of
// /fileSystems to f: a bucket registry (name + the mode the fake simulates
// the server deriving from the bucket's site) and a per-filesystem
// obs_buckets list. It does not implement the rest of /fileSystems or
// /objectStoreBuckets; register fakeFileSystems/fakeObjectStoreBuckets
// alongside it for a test that also needs those resources.
func fakeObjectStoreAttachments(t *testing.T, f *fakeWeka) func(fsUid, bucketUid string) {
	var bucketStore sync.Map // bucket uid -> struct{ Name, Mode string }
	var fsStore sync.Map     // fs uid -> *sync.Map of bucket uid -> fakeAttachedBucket

	fsAttachments := func(fsUid string) *sync.Map {
		v, _ := fsStore.LoadOrStore(fsUid, &sync.Map{})
		return v.(*sync.Map)
	}

	f.Handle("GET /objectStoreBuckets/{uid}", func(w http.ResponseWriter, r *http.Request) {
		v, ok := bucketStore.Load(r.PathValue("uid"))
		if !ok {
			writeError(w, http.StatusNotFound, "object store bucket not found")
			return
		}
		b := v.(struct{ Name, Mode string })
		writeData(w, map[string]any{"uid": r.PathValue("uid"), "name": b.Name})
	})
	f.Handle("POST /fileSystems/{uid}/objectStoreBuckets", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ObsName string `json:"obs_name"`
		}
		decodeBody(t, r, &body)
		var found string
		var mode string
		bucketStore.Range(func(k, v any) bool {
			b := v.(struct{ Name, Mode string })
			if b.Name == body.ObsName {
				found, mode = k.(string), b.Mode
				return false
			}
			return true
		})
		if found == "" {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("no object store bucket named %q", body.ObsName))
			return
		}
		fsAttachments(r.PathValue("uid")).Store(found, fakeAttachedBucket{Name: body.ObsName, Uid: found, Mode: mode})
		writeData(w, nil)
	})
	f.Handle("DELETE /fileSystems/{uid}/objectStoreBuckets/{obs_uid}", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := fsAttachments(r.PathValue("uid")).LoadAndDelete(r.PathValue("obs_uid")); !ok {
			writeError(w, http.StatusNotFound, "attachment not found")
			return
		}
		writeData(w, nil)
	})
	f.Handle("GET /fileSystems/{uid}", func(w http.ResponseWriter, r *http.Request) {
		var obsBuckets []fakeAttachedBucket
		fsAttachments(r.PathValue("uid")).Range(func(_, v any) bool {
			obsBuckets = append(obsBuckets, v.(fakeAttachedBucket))
			return true
		})
		writeData(w, map[string]any{"uid": r.PathValue("uid"), "obs_buckets": obsBuckets})
	})

	registerBucket := func(uid, name, mode string) {
		bucketStore.Store(uid, struct{ Name, Mode string }{name, mode})
	}
	registerBucket("bucket-local", "tiering", "WRITABLE")
	registerBucket("bucket-remote", "snapshots", "REMOTE")
	return func(fsUid, bucketUid string) {
		fsAttachments(fsUid).Delete(bucketUid)
	}
}

func TestObjectStoreAttachmentResource(t *testing.T) {
	f := newFakeWeka(t)
	drop := fakeObjectStoreAttachments(t, f)

	const fsUid = "fs-1"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: f.config(fmt.Sprintf(`
resource "weka_object_store_attachment" "tiering" {
  filesystem_id           = %q
  object_store_bucket_id  = "bucket-local"
}`, fsUid)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("weka_object_store_attachment.tiering", "id", fsUid+"/bucket-local"),
					resource.TestCheckResourceAttr("weka_object_store_attachment.tiering", "mode", "WRITABLE"),
				),
			},
			{
				ResourceName:      "weka_object_store_attachment.tiering",
				ImportState:       true,
				ImportStateId:     fsUid + "/bucket-local",
				ImportStateVerify: true,
			},
			{
				Config: f.config(fmt.Sprintf(`
resource "weka_object_store_attachment" "tiering" {
  filesystem_id           = %q
  object_store_bucket_id  = "bucket-local"
}
resource "weka_object_store_attachment" "snapshots" {
  filesystem_id           = %q
  object_store_bucket_id  = "bucket-remote"
}`, fsUid, fsUid)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("weka_object_store_attachment.snapshots", "mode", "REMOTE"),
				),
			},
			{
				// Deleted outside Terraform: Read drops it and the plan recreates it.
				PreConfig: func() { drop(fsUid, "bucket-local") },
				Config: f.config(fmt.Sprintf(`
resource "weka_object_store_attachment" "tiering" {
  filesystem_id           = %q
  object_store_bucket_id  = "bucket-local"
}
resource "weka_object_store_attachment" "snapshots" {
  filesystem_id           = %q
  object_store_bucket_id  = "bucket-remote"
}`, fsUid, fsUid)),
				Check: resource.TestCheckResourceAttr("weka_object_store_attachment.tiering", "mode", "WRITABLE"),
			},
		},
	})
}
