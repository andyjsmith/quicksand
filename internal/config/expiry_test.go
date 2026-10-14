package config

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDuration(t *testing.T) {
	for in, want := range map[string]time.Duration{
		`"36h"`: 36 * time.Hour, `"7d"`: 7 * 24 * time.Hour, `"1d12h"`: 36 * time.Hour, `"90m"`: 90 * time.Minute, `""`: 0,
	} {
		var d Duration
		if err := json.Unmarshal([]byte(in), &d); err != nil || time.Duration(d) != want {
			t.Errorf("%s = %v, %v; want %v", in, time.Duration(d), err, want)
		}
	}
	for _, in := range []string{`"7"`, `"d"`, `"-1h"`, `36`, `"1x"`} {
		var d Duration
		if err := json.Unmarshal([]byte(in), &d); err == nil {
			t.Errorf("%s = %v, want an error", in, time.Duration(d))
		}
	}
}

func TestExpiryAndQuotaDefaults(t *testing.T) {
	pve := `"pve": {"url": "https://pve:8006", "tokenId": "a@pve!b", "tokenSecret": "s", "pool": "p"}`
	var c Config
	json.Unmarshal([]byte(`{`+pve+`, "expiry": {"timeLimit": "3d", "maxExtends": 2}, "quota": {"user": 2}}`), &c)
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	if !c.Expiry.Enabled() || time.Duration(c.Expiry.ExtendDuration) != 24*time.Hour || *c.Expiry.MaxExtends != 2 {
		t.Errorf("expiry = %+v", c.Expiry)
	}
	if c.Quota.Global != nil || *c.Quota.User != 2 {
		t.Errorf("quota = %+v", c.Quota)
	}
	var none Config
	if none.Expiry.Enabled() {
		t.Error("no expiry section enables expiry")
	}
	for _, cfg := range []string{
		`"quota": {"user": -1}`,
		`"expiry": {"timeLimit": "1h", "maxExtends": -1}`,
		`"templates": [{"id": "t", "vmid": 100, "node": "n", "quota": {"global": -2}}], "vmids": {"from": 200, "to": 201}, "networks": [{"id": "n", "bridge": "vmbr0"}]`,
	} {
		var bad Config
		if err := json.Unmarshal([]byte(`{`+pve+`, `+cfg+`}`), &bad); err != nil {
			t.Fatal(err)
		}
		if err := bad.validate(); err == nil || !strings.Contains(err.Error(), "negative") {
			t.Errorf("%s: %v", cfg, err)
		}
	}
}
