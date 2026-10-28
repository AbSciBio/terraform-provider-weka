package provider

import (
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// omitempty is deliberately absent here: the live API sends a literal 0 for
// minuteOffset/retention rather than dropping the key (verified against
// sjc-prod-01's "share" policy, whose hourly minuteOffset is 0).
type fakeSchedule struct {
	Enable       bool    `json:"enable"`
	Time         string  `json:"time"`
	Hours        string  `json:"hours"`
	Days         string  `json:"days"`
	MinuteOffset float64 `json:"minuteOffset"`
	Retention    float64 `json:"retention"`
	Upload       string  `json:"upload"`
}

type fakePolicy struct {
	Name        string
	Description string
	Enabled     bool
	Daily       fakeSchedule
	Hourly      fakeSchedule
	Filesystems map[string]bool // fs uid -> true
}

// fakeSnapshotPolicies adds /fileSystems (list, for name<->uid lookups) and
// /snapshotPolicy* to f. fsNames maps filesystem uid to the name the fake
// reports, mirroring the live API returning names in the attached-filesystem
// list while accepting uids on attach/detach.
func fakeSnapshotPolicies(t *testing.T, f *fakeWeka, fsNames map[string]string) {
	var store sync.Map // uid -> *fakePolicy
	var seq int
	var mu sync.Mutex

	f.Handle("GET /fileSystems", func(w http.ResponseWriter, r *http.Request) {
		var list []map[string]any
		for uid, name := range fsNames {
			list = append(list, map[string]any{"uid": uid, "name": name})
		}
		writeData(w, list)
	})

	f.Handle("POST /snapshotPolicy", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name         string `json:"name"`
			Description  string `json:"description"`
			Enable       bool   `json:"enable"`
			PolicyParams struct {
				Daily  fakeSchedule `json:"daily"`
				Hourly fakeSchedule `json:"hourly"`
			} `json:"policy_params"`
		}
		decodeBody(t, r, &body)
		mu.Lock()
		seq++
		uid := fmt.Sprintf("policy-%d", seq)
		mu.Unlock()
		store.Store(uid, &fakePolicy{
			Name: body.Name, Description: body.Description, Enabled: body.Enable,
			Daily: body.PolicyParams.Daily, Hourly: body.PolicyParams.Hourly,
			Filesystems: map[string]bool{},
		})
		writeData(w, map[string]any{"uid": uid, "name": body.Name})
	})

	f.Handle("GET /snapshotPolicy/{uid}", func(w http.ResponseWriter, r *http.Request) {
		v, ok := store.Load(r.PathValue("uid"))
		if !ok {
			writeError(w, http.StatusNotFound, "snapshot policy not found")
			return
		}
		p := v.(*fakePolicy)
		var names []string
		for uid := range p.Filesystems {
			names = append(names, fsNames[uid])
		}
		disabled := fakeSchedule{Enable: false}
		writeData(w, map[string]any{
			"name": p.Name, "description": p.Description, "enabled": p.Enabled,
			"filesystems": names,
			"daily":       p.Daily, "hourly": p.Hourly,
			"weekly": disabled, "monthly": disabled, "periodic": disabled,
		})
	})

	f.Handle("PUT /snapshotPolicy/{uid}", func(w http.ResponseWriter, r *http.Request) {
		v, ok := store.Load(r.PathValue("uid"))
		if !ok {
			writeError(w, http.StatusNotFound, "snapshot policy not found")
			return
		}
		var body struct {
			NewName      *string `json:"new_name"`
			Description  *string `json:"description"`
			Enable       *bool   `json:"enable"`
			PolicyParams struct {
				Daily  fakeSchedule `json:"daily"`
				Hourly fakeSchedule `json:"hourly"`
			} `json:"policy_params"`
		}
		decodeBody(t, r, &body)
		p := v.(*fakePolicy)
		if body.NewName != nil {
			p.Name = *body.NewName
		}
		if body.Description != nil {
			p.Description = *body.Description
		}
		if body.Enable != nil {
			p.Enabled = *body.Enable
		}
		p.Daily, p.Hourly = body.PolicyParams.Daily, body.PolicyParams.Hourly
		writeData(w, map[string]any{"uid": r.PathValue("uid"), "name": p.Name})
	})

	f.Handle("DELETE /snapshotPolicy/{uid}", func(w http.ResponseWriter, r *http.Request) {
		v, ok := store.Load(r.PathValue("uid"))
		if !ok {
			writeError(w, http.StatusNotFound, "snapshot policy not found")
			return
		}
		if len(v.(*fakePolicy).Filesystems) > 0 {
			writeError(w, http.StatusBadRequest, "snapshot policy is attached to a filesystem")
			return
		}
		store.Delete(r.PathValue("uid"))
		writeData(w, nil)
	})

	f.Handle("POST /snapshotPolicy/{uid}/filesystems/attach", func(w http.ResponseWriter, r *http.Request) {
		v, ok := store.Load(r.PathValue("uid"))
		if !ok {
			writeError(w, http.StatusNotFound, "snapshot policy not found")
			return
		}
		var body struct {
			Filesystems []string `json:"filesystems"`
		}
		decodeBody(t, r, &body)
		p := v.(*fakePolicy)
		for _, uid := range body.Filesystems {
			p.Filesystems[uid] = true
		}
		writeData(w, body.Filesystems)
	})

	f.Handle("POST /snapshotPolicy/{uid}/filesystems/detach", func(w http.ResponseWriter, r *http.Request) {
		v, ok := store.Load(r.PathValue("uid"))
		if !ok {
			writeError(w, http.StatusNotFound, "snapshot policy not found")
			return
		}
		var body struct {
			Filesystems []string `json:"filesystems"`
		}
		decodeBody(t, r, &body)
		p := v.(*fakePolicy)
		for _, uid := range body.Filesystems {
			delete(p.Filesystems, uid)
		}
		writeData(w, body.Filesystems)
	})

	f.Handle("GET /snapshotPolicy", func(w http.ResponseWriter, r *http.Request) {
		var list []map[string]any
		store.Range(func(k, v any) bool {
			p := v.(*fakePolicy)
			list = append(list, map[string]any{"uid": k.(string), "name": p.Name})
			return true
		})
		writeData(w, list)
	})
}

func TestSnapshotPolicyResource(t *testing.T) {
	f := newFakeWeka(t)
	fsNames := map[string]string{"fs-1": "home", "fs-2": "share"}
	fakeSnapshotPolicies(t, f, fsNames)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: f.config(`
resource "weka_snapshot_policy" "home" {
  name        = "tfhome"
  filesystems = ["fs-1"]
  daily = {
    enable    = true
    time      = "07:00"
    days      = "all"
    retention = 7
    upload    = "remote"
  }
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("weka_snapshot_policy.home", "name", "tfhome"),
					resource.TestCheckResourceAttr("weka_snapshot_policy.home", "filesystems.#", "1"),
					resource.TestCheckResourceAttr("weka_snapshot_policy.home", "filesystems.0", "fs-1"),
					resource.TestCheckResourceAttr("weka_snapshot_policy.home", "daily.retention", "7"),
					resource.TestCheckResourceAttr("weka_snapshot_policy.home", "daily.upload", "remote"),
					resource.TestCheckNoResourceAttr("weka_snapshot_policy.home", "hourly.enable"),
				),
			},
			{
				ResourceName:      "weka_snapshot_policy.home",
				ImportState:       true,
				ImportStateId:     "tfhome",
				ImportStateVerify: true,
			},
			{
				// Add a second filesystem and an hourly schedule, matching
				// the real sjc-prod-01 "share" policy shape.
				Config: f.config(`
resource "weka_snapshot_policy" "home" {
  name        = "tfhome"
  filesystems = ["fs-1", "fs-2"]
  daily = {
    enable    = true
    time      = "07:00"
    days      = "all"
    retention = 7
    upload    = "remote"
  }
  hourly = {
    enable        = true
    hours         = "all"
    days          = "all"
    minute_offset = 0
    retention     = 24
    upload        = "remote"
  }
}`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("weka_snapshot_policy.home", "filesystems.#", "2"),
					resource.TestCheckResourceAttr("weka_snapshot_policy.home", "hourly.retention", "24"),
				),
			},
			// TestCase destroy (run automatically at the end) exercises
			// Delete detaching both filesystems before the API call that
			// rejects deleting a still-attached policy.
		},
	})
}
