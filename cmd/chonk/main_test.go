package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	for _, tc := range []struct {
		args []string
		out  string
		err  string
	}{
		{args: []string{"version"}, out: "chonk "},
		{args: []string{"--version"}, out: "chonk "},
		{args: []string{"-h"}, out: "Usage:"},
		{args: []string{"nope"}, err: `unknown command "nope"`},
		{args: []string{"why"}, err: "usage: chonk why PATH"},
		{args: []string{"--bogus"}, err: "flag provided but not defined"},
		{args: []string{"why", "/does/not/exist"}, err: "no such file"},
	} {
		var out bytes.Buffer
		err := run(context.Background(), tc.args, &out, &bytes.Buffer{})
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%v: err = %v, want %q", tc.args, err, tc.err)
			}
			continue
		}
		if err != nil || !strings.Contains(out.String(), tc.out) {
			t.Errorf("%v: %v %q", tc.args, err, out.String())
		}
	}
}

func TestWhyJSON(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	var out bytes.Buffer
	if err := run(context.Background(), []string{"why", dir, "--json"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"owner": "unclaimed"`) {
		t.Errorf("json = %s", out.String())
	}
}
